package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type SpeedResult struct {
	URL     string  `json:"url"`
	SNI     string  `json:"sni"`
	Bytes   int64   `json:"bytes"`
	BodyMS  float64 `json:"body_ms"`
	TotalMS float64 `json:"total_ms"`
	Mbps    float64 `json:"mbps"`
	Status  int     `json:"status"`
	Error   string  `json:"error,omitempty"`
}

func measureSpeed(ctx context.Context, t Target, c Config) SpeedResult {
	return speedWithDial(ctx, t, c, (&net.Dialer{}).DialContext)
}
func speedWithDial(ctx context.Context, t Target, c Config, dial func(context.Context, string, string) (net.Conn, error)) (out SpeedResult) {
	start := time.Now()
	defer func() { out.TotalMS = float64(time.Since(start).Microseconds()) / 1000 }()
	out.URL = c.SpeedURL
	u, e := url.Parse(c.SpeedURL)
	if e != nil {
		out.Error = e.Error()
		return
	}
	out.SNI = u.Hostname()
	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.SpeedTimeout)*time.Millisecond)
	defer cancel()
	tr := &http.Transport{Proxy: nil, DisableKeepAlives: true, DisableCompression: true, TLSClientConfig: &tls.Config{ServerName: u.Hostname(), InsecureSkipVerify: c.Insecure, MinVersion: tls.VersionTLS12}, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return dial(ctx, "tcp", net.JoinHostPort(t.IP, strconv.Itoa(t.Port)))
	}, MaxResponseHeaderBytes: 32768}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	req, e := http.NewRequestWithContext(ctx, "GET", c.SpeedURL, nil)
	if e != nil {
		out.Error = e.Error()
		return
	}
	req.Header.Set("User-Agent", "IPData-WEB/0.2")
	req.Header.Set("Accept-Encoding", "identity")
	resp, e := client.Do(req)
	if e != nil {
		out.Error = e.Error()
		return
	}
	defer resp.Body.Close()
	out.Status = resp.StatusCode
	if resp.StatusCode != 200 {
		out.Error = fmt.Sprintf("测速HTTP状态%d", resp.StatusCode)
		return
	}
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.Contains(ct, "text/html") || strings.Contains(ct, "json") || resp.Header.Get("Content-Encoding") != "" && resp.Header.Get("Content-Encoding") != "identity" {
		out.Error = "测速响应为页面/JSON或压缩数据"
		return
	}
	bodyStart := time.Now()
	out.Bytes, e = io.Copy(io.Discard, io.LimitReader(resp.Body, c.SpeedBytes))
	elapsed := time.Since(bodyStart)
	out.BodyMS = float64(elapsed.Nanoseconds()) / 1e6
	if e != nil {
		out.Error = e.Error()
		return
	}
	if out.Bytes != c.SpeedBytes {
		out.Error = fmt.Sprintf("测速数据不足：%d/%d", out.Bytes, c.SpeedBytes)
		return
	}
	if elapsed > 0 {
		out.Mbps = float64(out.Bytes) * 8 / elapsed.Seconds() / 1e6
	}
	return
}
