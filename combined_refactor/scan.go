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
	MaxTargets   int    `json:"max_targets,omitempty"`
	Mode         string `json:"mode,omitempty"`
	NodeTemplate string `json:"vless_template,omitempty"`
	NodeCheckURL string `json:"node_check_url,omitempty"`
	NodeTimeout  int    `json:"node_timeout_ms,omitempty"`
	SpeedEnabled *bool  `json:"speed_enabled,omitempty"`
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
	Node         *NodeResult   `json:"vless,omitempty"`
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
	return Config{MaxTargets: defaultMaxTargets, Mode: "cf", NodeCheckURL: "https://www.cloudflare.com/cdn-cgi/trace", NodeTimeout: 10000, Ports: "443,8443", SNI: "www.cloudflare.com", VerifySNI: "one.one.one.one", Concurrency: 32, Rate: 50, Timeout: 4000, Rounds: 3, SpeedBytes: 1048576, SpeedTimeout: 10000}
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
	p, e := planTargets(c)
	if e != nil {
		return nil, e
	}
	return p.expanded(), nil
}
func validateConfig(c Config) error {
	mode := scanMode(c)
	if mode != "cf" && mode != "combined" && mode != "vless" {
		return fmt.Errorf("测试模式须为cf、combined或vless")
	}
	if mode != "cf" {
		if _, e := parseNodeTemplate(c.NodeTemplate); e != nil {
			return e
		}
		if !validTestURL(c.NodeCheckURL) {
			return fmt.Errorf("VLESS真连接地址须为不含凭据的有效HTTPS URL")
		}
		if c.NodeTimeout < 100 || c.NodeTimeout > 60000 {
			return fmt.Errorf("VLESS单次超时需为100–60000ms")
		}
	}
	if wantSpeed(c) {
		u, e := url.Parse(c.SpeedURL)
		if mode == "cf" && (e != nil || u.Scheme != "https" || !hostname(u.Hostname()) || u.User != nil || u.Fragment != "" || (u.Port() != "" && u.Port() != "443")) {
			return fmt.Errorf("测速地址需为不含用户凭据的 HTTPS 域名地址（默认443端口）")
		}
		if mode != "cf" && !validTestURL(c.SpeedURL) {
			return fmt.Errorf("VLESS测速地址需为有效HTTPS URL")
		}
		if c.SpeedBytes < 1024 || c.SpeedBytes > 32*1024*1024 || c.SpeedTimeout < 100 || c.SpeedTimeout > 60000 {
			return fmt.Errorf("测速字节数1024–33554432，超时100–60000ms")
		}
	}
	if c.Concurrency < 1 || c.Rate < 0 || c.MaxTargets < 0 || c.Timeout < 100 || c.Timeout > 30000 || c.Rounds < 1 || c.Rounds > 10 {
		return fmt.Errorf("并发须为正整数，速率须为非负整数（0不限速），目标上限不可为负；超时100–30000ms，轮数1–10")
	}
	if mode != "vless" && (!hostname(c.SNI) || (c.VerifySNI != "" && (!hostname(c.VerifySNI) || strings.EqualFold(c.SNI, c.VerifySNI)))) {
		return fmt.Errorf("请输入有效且不同的 SNI 域名")
	}
	return nil
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
	scanWithNodeChecks(ctx, c, targets, emit, probeNode, measureNodeSpeed)
}
func scanWithNodeChecks(ctx context.Context, c Config, targets []Target, emit func(Result), check func(context.Context, Config, Target, *nodeTemplate) NodeObservation, download func(context.Context, Config, Target, *nodeTemplate) SpeedResult) {
	scanSource(ctx, c, len(targets), func(yield func(Target) bool) {
		for _, t := range targets {
			if !yield(t) {
				return
			}
		}
	}, emit, check, download)
}
func scanTargetPlan(ctx context.Context, c Config, plan *targetPlan, seen map[string]bool, emit func(Result)) {
	scanSource(ctx, c, max(0, plan.Count-len(seen)), func(yield func(Target) bool) {
		plan.each(func(t Target) bool {
			if ctx.Err() != nil {
				return false
			}
			if seen[key(t.IP, t.Port)] {
				return true
			}
			return yield(t)
		})
	}, emit, probeNode, measureNodeSpeed)
}
func scanSource(ctx context.Context, c Config, count int, source func(func(Target) bool), emit func(Result), check func(context.Context, Config, Target, *nodeTemplate) NodeObservation, download func(context.Context, Config, Target, *nodeTemplate) SpeedResult) {
	if count == 0 {
		return
	}

	mode := scanMode(c)
	var node *nodeTemplate
	if mode != "cf" {
		var e error
		node, e = parseNodeTemplate(c.NodeTemplate)
		if e != nil {
			return
		}
	}
	queue := make(chan Target)
	var wg sync.WaitGroup
	var emitMu sync.Mutex
	limiter := newRequestPacer(c.Rate)
	pace := func() bool { return limiter.wait(ctx) }
	request := func(t Target, sni string) Observation {
		if !pace() {
			return Observation{SNI: sni, Error: ctx.Err().Error()}
		}
		return observe(ctx, t, sni, c)
	}
	for i := 0; i < min(c.Concurrency, count); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range queue {
				r := Result{IP: t.IP, Port: t.Port, Provider: c.Provider, Rounds: c.Rounds, Evidence: "none", Observations: []Observation{}}
				times := []float64{}
				allTrace := true
				if mode != "vless" {
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
				} else {
					r.Evidence = "not_run"
				}
				if node != nil && (mode == "vless" || r.Success > 0) && ctx.Err() == nil {
					nr := &NodeResult{Rounds: c.Rounds, Observations: []NodeObservation{}}
					for i := 0; i < c.Rounds; i++ {
						if !pace() {
							break
						}
						o := check(ctx, c, t, node)
						nr.Observations = append(nr.Observations, o)
						if o.Error == "" {
							nr.Success++
						}
					}
					nr.Median = nodeMedian(nr.Observations)
					r.Node = nr
					if wantSpeed(c) && nr.Success > 0 && pace() {
						s := download(ctx, c, t, node)
						r.Speed = &s
					}
				}
				if mode == "cf" && wantSpeed(c) && (r.Evidence == "trace_valid" || r.Evidence == "dual_verified") && ctx.Err() == nil {
					if pace() {
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
	source(func(t Target) bool {
		select {
		case <-ctx.Done():
			return false
		case queue <- t:
			return true
		}
	})
	close(queue)
	wg.Wait()
}
