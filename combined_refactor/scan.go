package main

import (
	"context"
	"crypto/tls"
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
)

type Config struct {
	Targets      string `json:"targets"`
	Ports        string `json:"ports"`
	SNI          string `json:"sni"`
	VerifySNI    string `json:"verify_sni"`
	Concurrency  int    `json:"concurrency"`
	Rate         int    `json:"rate"`
	Timeout      int    `json:"timeout_ms"`
	Rounds       int    `json:"rounds"`
	Insecure     bool   `json:"insecure"`
	Provider     string `json:"provider"`
	SpeedURL     string `json:"speed_url"`
	SpeedBytes   int64  `json:"speed_bytes"`
	SpeedTimeout int    `json:"speed_timeout_ms"`
}
type Target struct {
	IP   string
	Port int
}
type Observation struct {
	SNI    string  `json:"sni"`
	Status int     `json:"status"`
	Server string  `json:"server"`
	Ray    string  `json:"cf_ray"`
	Colo   string  `json:"colo"`
	ExitIP string  `json:"exit_ip"`
	CF     bool    `json:"cf"`
	Trace  bool    `json:"trace_valid"`
	TCP    float64 `json:"tcp_ms"`
	TLS    float64 `json:"tls_ms"`
	TTFB   float64 `json:"ttfb_ms"`
	Total  float64 `json:"total_ms"`
	Error  string  `json:"error,omitempty"`
}
type Result struct {
	IP           string        `json:"ip"`
	Port         int           `json:"port"`
	Provider     string        `json:"provider"`
	Evidence     string        `json:"evidence"`
	Success      int           `json:"success"`
	Rounds       int           `json:"rounds"`
	Median       float64       `json:"median_ms"`
	Observations []Observation `json:"observations"`
	Speed        *SpeedResult  `json:"speed,omitempty"`
}

func defaults() Config {
	return Config{Ports: "443,8443", SNI: "www.cloudflare.com", VerifySNI: "one.one.one.one", Concurrency: 32, Rate: 50, Timeout: 4000, Rounds: 3, SpeedBytes: 1048576, SpeedTimeout: 10000}
}
func hostname(s string) bool {
	if len(s) > 253 || !strings.Contains(s, ".") {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}
func prepare(c Config) ([]Target, error) {
	if c.SpeedURL != "" {
		u, e := url.Parse(c.SpeedURL)
		if e != nil || u.Scheme != "https" || !hostname(u.Hostname()) || u.User != nil || u.Fragment != "" || (u.Port() != "" && u.Port() != "443") {
			return nil, fmt.Errorf("测速地址需为不含用户凭据的 HTTPS 域名地址（默认443端口）")
		}
		if c.SpeedBytes < 1024 || c.SpeedBytes > 32*1024*1024 || c.SpeedTimeout < 100 || c.SpeedTimeout > 60000 {
			return nil, fmt.Errorf("测速字节数1024–33554432，超时100–60000ms")
		}
	}
	if c.Concurrency < 1 || c.Concurrency > 512 || c.Rate < 1 || c.Rate > 2000 || c.Timeout < 100 || c.Timeout > 30000 || c.Rounds < 1 || c.Rounds > 10 {
		return nil, fmt.Errorf("并发 1–512，速率 1–2000，超时 100–30000ms，轮数 1–10")
	}
	if !hostname(c.SNI) || (c.VerifySNI != "" && (!hostname(c.VerifySNI) || strings.EqualFold(c.SNI, c.VerifySNI))) {
		return nil, fmt.Errorf("请输入有效且不同的 SNI 域名")
	}
	ports := []int{}
	seenPorts := map[int]bool{}
	for _, s := range strings.Split(c.Ports, ",") {
		p, e := strconv.Atoi(strings.TrimSpace(s))
		if e != nil || p < 1 || p > 65535 {
			return nil, fmt.Errorf("端口无效")
		}
		if !seenPorts[p] {
			ports = append(ports, p)
			seenPorts[p] = true
		}
	}
	if len(ports) > 16 {
		return nil, fmt.Errorf("最多16个端口")
	}
	out := []Target{}
	seen := map[netip.Addr]bool{}
	add := func(a netip.Addr) error {
		a = a.Unmap()
		if seen[a] {
			return nil
		}
		if len(out)+len(ports) > 100000 {
			return fmt.Errorf("最多100000个 IP:端口，请缩小网段")
		}
		seen[a] = true
		for _, p := range ports {
			out = append(out, Target{a.String(), p})
		}
		return nil
	}
	for _, line := range strings.Split(c.Targets, "\n") {
		line = strings.SplitN(line, "#", 2)[0]
		for _, s := range strings.FieldsFunc(line, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\r' }) {
			if a, e := netip.ParseAddr(s); e == nil {
				if e = add(a); e != nil {
					return nil, e
				}
				continue
			}
			p, e := netip.ParsePrefix(s)
			if e != nil {
				return nil, fmt.Errorf("无效IP/CIDR：%s", s)
			}
			p = p.Masked()
			if p.Addr().BitLen()-p.Bits() > 16 {
				return nil, fmt.Errorf("单个网段最多65536地址")
			}
			for a := p.Addr(); p.Contains(a); a = a.Next() {
				if e = add(a); e != nil {
					return nil, e
				}
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("请提供云厂商 IP 或 CIDR")
	}
	return out, nil
}
func observe(ctx context.Context, t Target, sni string, c Config) Observation {
	return observeWithDial(ctx, t, sni, c, (&net.Dialer{}).DialContext)
}
func observeWithDial(ctx context.Context, t Target, sni string, c Config, dial func(context.Context, string, string) (net.Conn, error)) (o Observation) {
	o.SNI = sni
	start := time.Now()

	var traceMu sync.Mutex
	var tcpStart, tlsStart time.Time
	var tcpMS, tlsMS, ttfbMS float64
	defer func() {
		traceMu.Lock()
		o.TCP = tcpMS
		o.TLS = tlsMS
		o.TTFB = ttfbMS
		traceMu.Unlock()
		o.Total = float64(time.Since(start).Microseconds()) / 1000
	}()
	trace := &httptrace.ClientTrace{
		ConnectStart: func(_, _ string) { traceMu.Lock(); tcpStart = time.Now(); traceMu.Unlock() },
		ConnectDone: func(_, _ string, _ error) {
			traceMu.Lock()
			tcpMS = float64(time.Since(tcpStart).Microseconds()) / 1000
			traceMu.Unlock()
		},
		TLSHandshakeStart: func() { traceMu.Lock(); tlsStart = time.Now(); traceMu.Unlock() },
		TLSHandshakeDone: func(_ tls.ConnectionState, _ error) {
			traceMu.Lock()
			tlsMS = float64(time.Since(tlsStart).Microseconds()) / 1000
			traceMu.Unlock()
		},
		GotFirstResponseByte: func() { traceMu.Lock(); ttfbMS = float64(time.Since(start).Microseconds()) / 1000; traceMu.Unlock() },
	}

	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.Timeout)*time.Millisecond)
	defer cancel()
	tr := &http.Transport{Proxy: nil, DisableKeepAlives: true, TLSClientConfig: &tls.Config{ServerName: sni, InsecureSkipVerify: c.Insecure, MinVersion: tls.VersionTLS12}, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return dial(ctx, "tcp", net.JoinHostPort(t.IP, strconv.Itoa(t.Port)))
	}, MaxResponseHeaderBytes: 32768}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	req, e := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), "GET", "https://"+sni+"/cdn-cgi/trace", nil)
	if e != nil {
		o.Error = e.Error()
		return
	}
	req.Header.Set("User-Agent", "IPData-WEB/0.1")
	resp, e := client.Do(req)
	if e != nil {
		o.Error = e.Error()
		return
	}
	defer resp.Body.Close()
	o.Status = resp.StatusCode
	o.Server = resp.Header.Get("Server")
	o.Ray = resp.Header.Get("CF-Ray")
	o.CF = strings.EqualFold(o.Server, "cloudflare") || o.Ray != ""
	body, e := io.ReadAll(io.LimitReader(resp.Body, 65537))
	if e != nil {
		o.Error = e.Error()
		return
	}
	if len(body) > 65536 {
		o.Error = "response too large"
		return
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(body), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if ok {
			values[k] = strings.TrimSpace(v)
		}
	}
	o.Colo = values["colo"]
	o.ExitIP = values["ip"]
	_, iperr := netip.ParseAddr(o.ExitIP)
	o.Trace = resp.StatusCode == 200 && strings.EqualFold(values["h"], sni) && len(o.Colo) == 3 && values["fl"] != "" && iperr == nil
	o.CF = o.CF || o.Trace
	return
}
func scan(ctx context.Context, c Config, targets []Target, emit func(Result)) {
	queue := make(chan Target)
	var wg sync.WaitGroup
	var emitMu sync.Mutex
	ticker := time.NewTicker(time.Second / time.Duration(c.Rate))
	defer ticker.Stop()
	request := func(t Target, sni string) Observation {
		select {
		case <-ctx.Done():
			return Observation{SNI: sni, Error: ctx.Err().Error()}
		case <-ticker.C:
			return observe(ctx, t, sni, c)
		}
	}
	for i := 0; i < c.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range queue {
				r := Result{IP: t.IP, Port: t.Port, Provider: c.Provider, Rounds: c.Rounds, Evidence: "none", Observations: []Observation{}}
				times := []float64{}
				allTrace := true
				for j := 0; j < c.Rounds; j++ {
					if ctx.Err() != nil {
						break
					}
					o := request(t, c.SNI)
					r.Observations = append(r.Observations, o)
					if o.CF && o.Error == "" {
						r.Success++
						times = append(times, o.TTFB)
						if r.Evidence == "none" {
							r.Evidence = "cf_headers"
						}
					}
					allTrace = allTrace && o.Trace && o.Error == ""
				}
				if allTrace && r.Success == c.Rounds {
					r.Evidence = "trace_valid"
					if c.VerifySNI != "" {
						o := request(t, c.VerifySNI)
						r.Observations = append(r.Observations, o)
						if o.Trace && o.Error == "" {
							r.Evidence = "dual_verified"
						}
					}
				}
				if len(times) > 0 {
					sort.Float64s(times)
					n := len(times)
					r.Median = times[n/2]
					if n%2 == 0 {
						r.Median = (times[n/2-1] + times[n/2]) / 2
					}
				}
				if c.SpeedURL != "" && (r.Evidence == "trace_valid" || r.Evidence == "dual_verified") && ctx.Err() == nil {
					select {
					case <-ctx.Done():
					case <-ticker.C:
						s := measureSpeed(ctx, t, c)
						r.Speed = &s
					}
				}
				if ctx.Err() == nil {
					emitMu.Lock()
					emit(r)
					emitMu.Unlock()
				}
			}
		}()
	}
loop:
	for _, t := range targets {
		select {
		case <-ctx.Done():
			break loop
		case queue <- t:
		}
	}
	close(queue)
	wg.Wait()
}
