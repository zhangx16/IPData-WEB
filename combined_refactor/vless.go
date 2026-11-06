package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/netip"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type nodeTemplate struct {
	UUID      [16]byte
	UUIDText  string
	SNI       string
	Host      string
	Path      string
	Transport string
	ALPN      []string
}
type NodeObservation struct {
	Status int     `json:"status"`
	TTFB   float64 `json:"ttfb_ms"`
	Total  float64 `json:"total_ms"`
	ExitIP string  `json:"exit_ip,omitempty"`
	Colo   string  `json:"colo,omitempty"`
	Error  string  `json:"error,omitempty"`
}
type NodeResult struct {
	Success      int               `json:"success"`
	Rounds       int               `json:"rounds"`
	Median       float64           `json:"median_ms"`
	Observations []NodeObservation `json:"observations"`
}

func scanMode(c Config) string {
	if c.Mode == "" {
		return "cf"
	}
	return c.Mode
}
func wantSpeed(c Config) bool {
	if c.SpeedEnabled != nil {
		return *c.SpeedEnabled
	}
	return c.SpeedURL != ""
}
func resultHit(r Result) bool {
	if r.Node != nil {
		return r.Node.Success > 0
	}
	return r.Success > 0
}
func validTLSName(name string) bool {
	if _, e := netip.ParseAddr(name); e == nil {
		return true
	}
	return hostname(name)
}
func parseNodeTemplate(raw string) (*nodeTemplate, error) {
	fail := func(reason string) (*nodeTemplate, error) { return nil, fmt.Errorf("VLESS模板：%s", reason) }
	if len(raw) > 8192 {
		return fail("链接过长")
	}
	u, e := url.Parse(strings.TrimSpace(raw))
	if e != nil || u.Scheme != "vless" || u.User == nil || u.Opaque != "" {
		return fail("需要有效的vless://分享链接")
	}
	if _, ok := u.User.Password(); ok {
		return fail("不支持密码形式的用户信息")
	}
	if u.Hostname() == "" {
		return fail("缺少服务器地址")
	}
	p, e := strconv.Atoi(u.Port())
	if e != nil || p < 1 || p > 65535 {
		return fail("模板服务器端口无效")
	}
	id := u.User.Username()
	compact := strings.ReplaceAll(id, "-", "")
	b, e := hex.DecodeString(compact)
	if e != nil || len(b) != 16 {
		return fail("UUID无效")
	}
	if len(id) != 32 && (len(id) != 36 || id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-') {
		return fail("UUID格式无效")
	}
	q, e := url.ParseQuery(u.RawQuery)
	if e != nil {
		return fail("查询参数编码无效")
	}
	for _, v := range q {
		if len(v) > 1 {
			return fail("不支持重复查询参数")
		}
	}
	if security := q.Get("security"); security != "tls" {
		return fail("目前仅支持security=tls，不支持Reality或明文模板")
	}
	if encryption := q.Get("encryption"); encryption != "" && encryption != "none" {
		return fail("目前仅支持encryption=none")
	}
	for _, k := range []string{"flow", "fp", "pbk", "sid", "packetEncoding", "packet_encoding", "ed", "eh", "serviceName"} {
		if q.Get(k) != "" {
			return fail("暂不支持参数 " + k)
		}
	}
	if v := q.Get("headerType"); v != "" && v != "none" {
		return fail("暂不支持TCP头部伪装")
	}
	if q.Get("allowInsecure") == "1" || q.Get("allowInsecure") == "true" {
		return fail("请使用页面的外层证书校验选项，不在模板中设置allowInsecure")
	}
	tr := q.Get("type")
	if tr == "" {
		tr = "tcp"
	}
	if tr != "ws" && tr != "tcp" {
		return fail("目前仅支持WS+TLS或TCP+TLS")
	}
	n := &nodeTemplate{UUIDText: id, Transport: tr, Path: q.Get("path"), Host: q.Get("host"), SNI: q.Get("sni")}
	copy(n.UUID[:], b)
	if n.SNI == "" {
		n.SNI = q.Get("serverName")
	}
	if n.SNI == "" {
		n.SNI = n.Host
	}
	if n.SNI == "" {
		n.SNI = u.Hostname()
	}
	if !validTLSName(n.SNI) {
		return fail("SNI无效")
	}
	if n.Host == "" {
		n.Host = n.SNI
	}
	host := n.Host
	if strings.Contains(host, ":") {
		if h, port, e := net.SplitHostPort(host); e == nil {
			host = h
			v, pe := strconv.Atoi(port)
			if pe != nil || v < 1 || v > 65535 {
				return fail("Host端口无效")
			}
		}
	}
	if !validTLSName(host) || strings.ContainsAny(n.Host, "\r\n\t /\\") {
		return fail("Host无效")
	}
	if n.Path == "" {
		n.Path = "/"
	}
	if len(n.Path) > 4096 || !strings.HasPrefix(n.Path, "/") || strings.ContainsAny(n.Path, "\r\n") {
		return fail("WebSocket路径无效")
	}
	path, e := url.ParseRequestURI(n.Path)
	if e != nil || path.IsAbs() || path.Host != "" || path.Fragment != "" {
		return fail("WebSocket路径无效")
	}
	if tr == "ws" {
		if q.Get("alpn") != "" && !strings.Contains(","+q.Get("alpn")+",", ",http/1.1,") {
			return fail("WebSocket模板需要HTTP/1.1 ALPN")
		}
		n.ALPN = []string{"http/1.1"}
	} else if q.Get("alpn") != "" {
		for _, v := range strings.Split(q.Get("alpn"), ",") {
			if v != "http/1.1" && v != "h2" {
				return fail("不支持该ALPN")
			}
			n.ALPN = append(n.ALPN, v)
		}
	}
	return n, nil
}
func validTestURL(raw string) bool {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || !validTLSName(u.Hostname()) || u.User != nil || u.Fragment != "" {
		return false
	}
	if u.Port() != "" {
		p, e := strconv.Atoi(u.Port())
		if e != nil || p < 1 || p > 65535 {
			return false
		}
	}
	return true
}
func nodeError(e error, n *nodeTemplate) string {
	if e == nil {
		return ""
	}
	s := e.Error()
	for _, v := range []string{n.UUIDText, hex.EncodeToString(n.UUID[:]), n.Path, n.SNI, n.Host} {
		if len(v) > 1 {
			s = strings.ReplaceAll(s, v, "<模板参数>")
		}
	}
	return s
}

// wsStream exposes RFC6455 binary messages as a byte stream for VLESS/TLS.
// Gorilla handles masking, ping/pong, fragmentation and frame validation.
type wsStream struct {
	*websocket.Conn
	readMu  sync.Mutex
	writeMu sync.Mutex
	reader  io.Reader
}

func (w *wsStream) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	w.readMu.Lock()
	defer w.readMu.Unlock()
	for {
		if w.reader == nil {
			kind, r, e := w.NextReader()
			if e != nil {
				return 0, e
			}
			if kind != websocket.BinaryMessage {
				return 0, fmt.Errorf("WebSocket返回非二进制数据")
			}
			w.reader = r
		}
		n, e := w.reader.Read(p)
		if e == io.EOF {
			w.reader = nil
			if n > 0 {
				return n, nil
			}
			continue
		}
		return n, e
	}
}
func (w *wsStream) Write(p []byte) (int, error) {
	w.writeMu.Lock()
	defer w.writeMu.Unlock()
	if e := w.WriteMessage(websocket.BinaryMessage, p); e != nil {
		return 0, e
	}
	return len(p), nil
}
func (w *wsStream) LocalAddr() net.Addr  { return w.UnderlyingConn().LocalAddr() }
func (w *wsStream) RemoteAddr() net.Addr { return w.UnderlyingConn().RemoteAddr() }
func (w *wsStream) SetDeadline(t time.Time) error {
	if e := w.SetReadDeadline(t); e != nil {
		return e
	}
	return w.SetWriteDeadline(t)
}

type vlessStream struct {
	net.Conn
	header   []byte
	writeMu  sync.Mutex
	readMu   sync.Mutex
	sent     bool
	received bool
	readErr  error
}

func (v *vlessStream) Write(p []byte) (int, error) {
	v.writeMu.Lock()
	defer v.writeMu.Unlock()
	if v.sent {
		return v.Conn.Write(p)
	}
	v.sent = true
	wire := make([]byte, 0, len(v.header)+len(p))
	wire = append(wire, v.header...)
	wire = append(wire, p...)
	n, e := v.Conn.Write(wire)
	return max(0, min(len(p), n-len(v.header))), e
}
func (v *vlessStream) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	v.readMu.Lock()
	defer v.readMu.Unlock()
	if v.readErr != nil {
		return 0, v.readErr
	}
	if !v.received {
		v.received = true
		var h [2]byte
		if _, e := io.ReadFull(v.Conn, h[:]); e != nil {
			v.readErr = e
			return 0, e
		}
		if h[0] != 0 {
			v.readErr = fmt.Errorf("VLESS响应版本无效")
			return 0, v.readErr
		}
		if _, e := io.CopyN(io.Discard, v.Conn, int64(h[1])); e != nil {
			v.readErr = e
			return 0, e
		}
	}
	return v.Conn.Read(p)
}
func vlessHeader(n *nodeTemplate, address string) ([]byte, error) {
	host, port, e := net.SplitHostPort(address)
	if e != nil {
		return nil, e
	}
	p, e := strconv.Atoi(port)
	if e != nil || p < 1 || p > 65535 {
		return nil, fmt.Errorf("目标端口无效")
	}
	h := append([]byte{0}, n.UUID[:]...)
	h = append(h, 0, 1, byte(p>>8), byte(p))
	if ip, e := netip.ParseAddr(host); e == nil {
		ip = ip.Unmap()
		if ip.Is4() {
			h = append(h, 1)
		} else {
			h = append(h, 3)
		}
		h = append(h, ip.AsSlice()...)
	} else {
		if !hostname(host) || len(host) > 255 {
			return nil, fmt.Errorf("目标域名无效")
		}
		h = append(h, 2, byte(len(host)))
		h = append(h, host...)
	}
	return h, nil
}
func nodeHTTPClient(c Config, t Target, n *nodeTemplate, roots *x509.CertPool) *http.Client {
	dial := func(ctx context.Context, _, address string) (net.Conn, error) {
		h, e := vlessHeader(n, address)
		if e != nil {
			return nil, e
		}
		handshake, cancel := context.WithTimeout(ctx, time.Duration(c.NodeTimeout)*time.Millisecond)
		defer cancel()
		target := net.JoinHostPort(t.IP, strconv.Itoa(t.Port))
		outerTLS := &tls.Config{ServerName: n.SNI, InsecureSkipVerify: c.Insecure, MinVersion: tls.VersionTLS12, RootCAs: roots, NextProtos: n.ALPN}
		var conn net.Conn
		if n.Transport == "ws" {
			path, _ := url.ParseRequestURI(n.Path)
			u := url.URL{Scheme: "wss", Host: n.SNI, Path: path.Path, RawPath: path.RawPath, RawQuery: path.RawQuery}
			d := websocket.Dialer{Proxy: nil, HandshakeTimeout: time.Duration(c.NodeTimeout) * time.Millisecond, TLSClientConfig: outerTLS, NetDialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "tcp", target)
			}}
			headers := http.Header{}
			headers.Set("Host", n.Host)
			ws, resp, e := d.DialContext(handshake, u.String(), headers)
			if e != nil {
				if resp != nil && resp.Body != nil {
					resp.Body.Close()
				}
				return nil, e
			}
			ws.SetReadLimit(64 * 1024 * 1024)
			conn = &wsStream{Conn: ws}
		} else {
			raw, e := (&net.Dialer{}).DialContext(handshake, "tcp", target)
			if e != nil {
				return nil, e
			}
			tlsConn := tls.Client(raw, outerTLS)
			if e = tlsConn.HandshakeContext(handshake); e != nil {
				raw.Close()
				return nil, e
			}
			conn = tlsConn
		}
		return &vlessStream{Conn: conn, header: h}, nil
	}
	tr := &http.Transport{Proxy: nil, DialContext: dial, DisableKeepAlives: true, DisableCompression: true, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}, TLSHandshakeTimeout: time.Duration(c.NodeTimeout) * time.Millisecond, MaxResponseHeaderBytes: 32768}
	return &http.Client{Transport: tr, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
}
func probeNode(ctx context.Context, c Config, t Target, n *nodeTemplate) NodeObservation {
	return probeNodeWithRoots(ctx, c, t, n, nil)
}
func probeNodeWithRoots(ctx context.Context, c Config, t Target, n *nodeTemplate, roots *x509.CertPool) (out NodeObservation) {
	start := time.Now()
	var mu sync.Mutex
	var ttfb float64
	defer func() {
		mu.Lock()
		out.TTFB = ttfb
		mu.Unlock()
		out.Total = float64(time.Since(start).Nanoseconds()) / 1e6
	}()
	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.NodeTimeout)*time.Millisecond)
	defer cancel()
	trace := &httptrace.ClientTrace{GotFirstResponseByte: func() { mu.Lock(); ttfb = float64(time.Since(start).Nanoseconds()) / 1e6; mu.Unlock() }}
	req, e := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), "GET", c.NodeCheckURL, nil)
	if e != nil {
		out.Error = "真连接测试地址无效"
		return
	}
	req.Header.Set("User-Agent", "IPData-WEB/0.3")
	req.Header.Set("Accept-Encoding", "identity")
	client := nodeHTTPClient(c, t, n, roots)
	defer client.CloseIdleConnections()
	resp, e := client.Do(req)
	if e != nil {
		out.Error = nodeError(e, n)
		return
	}
	defer resp.Body.Close()
	out.Status = resp.StatusCode
	if resp.StatusCode != 200 && resp.StatusCode != 204 {
		out.Error = fmt.Sprintf("代理HTTPS状态%d", resp.StatusCode)
		return
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, 65537))
	if e != nil {
		out.Error = nodeError(e, n)
		return
	}
	if len(b) > 65536 {
		out.Error = "真连接响应过大"
		return
	}
	u, _ := url.Parse(c.NodeCheckURL)
	if u.Path == "/cdn-cgi/trace" {
		values := map[string]string{}
		for _, line := range strings.Split(string(b), "\n") {
			k, v, ok := strings.Cut(line, "=")
			if ok {
				values[k] = strings.TrimSpace(v)
			}
		}
		out.ExitIP = values["ip"]
		out.Colo = values["colo"]
		_, e := netip.ParseAddr(out.ExitIP)
		if e != nil || !strings.EqualFold(values["h"], u.Hostname()) || values["fl"] == "" || len(out.Colo) != 3 {
			out.Error = "代理返回的Trace无效"
		}
	}
	return
}
func nodeMedian(obs []NodeObservation) float64 {
	var values []float64
	for _, o := range obs {
		if o.Error == "" {
			values = append(values, o.TTFB)
		}
	}
	if len(values) == 0 {
		return 0
	}
	sort.Float64s(values)
	n := len(values)
	if n%2 == 0 {
		return (values[n/2-1] + values[n/2]) / 2
	}
	return values[n/2]
}

func measureNodeSpeed(ctx context.Context, c Config, t Target, n *nodeTemplate) SpeedResult {
	return nodeSpeedWithRoots(ctx, c, t, n, nil)
}
func nodeSpeedWithRoots(ctx context.Context, c Config, t Target, n *nodeTemplate, roots *x509.CertPool) (out SpeedResult) {
	out.Via = "vless"
	out.URL = c.SpeedURL
	u, _ := url.Parse(c.SpeedURL)
	out.SNI = u.Hostname()
	start := time.Now()
	defer func() { out.TotalMS = float64(time.Since(start).Nanoseconds()) / 1e6 }()
	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.SpeedTimeout)*time.Millisecond)
	defer cancel()
	client := nodeHTTPClient(c, t, n, roots)
	defer client.CloseIdleConnections()
	req, e := http.NewRequestWithContext(ctx, "GET", c.SpeedURL, nil)
	if e != nil {
		out.Error = "测速URL无效"
		return
	}
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("User-Agent", "IPData-WEB/0.3")
	resp, e := client.Do(req)
	if e != nil {
		out.Error = nodeError(e, n)
		return
	}
	defer resp.Body.Close()
	out.Status = resp.StatusCode
	if out.Status != 200 {
		out.Error = fmt.Sprintf("代理测速HTTP状态%d", out.Status)
		return
	}
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	encoding := resp.Header.Get("Content-Encoding")
	if strings.Contains(ct, "text/html") || strings.Contains(ct, "json") || encoding != "" && encoding != "identity" {
		out.Error = "代理测速响应为页面/JSON或压缩数据"
		return
	}
	bodyStart := time.Now()
	out.Bytes, e = io.Copy(io.Discard, io.LimitReader(resp.Body, c.SpeedBytes))
	elapsed := time.Since(bodyStart)
	out.BodyMS = float64(elapsed.Nanoseconds()) / 1e6
	if e != nil {
		out.Error = nodeError(e, n)
		return
	}
	if out.Bytes != c.SpeedBytes {
		out.Error = fmt.Sprintf("代理测速数据不足：%d/%d", out.Bytes, c.SpeedBytes)
		return
	}
	if elapsed > 0 {
		out.Mbps = float64(out.Bytes) * 8 / elapsed.Seconds() / 1e6
	}
	return
}
