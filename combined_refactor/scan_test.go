package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestPrepare(t *testing.T) {
	c := defaults()
	c.Targets = "192.0.2.0/30\n192.0.2.1 # duplicate\n2001:db8::1"
	ts, e := prepare(c)
	if e != nil || len(ts) != 10 {
		t.Fatalf("%d %v", len(ts), e)
	}
	c.Targets = "0.0.0.0/0"
	if _, e = prepare(c); e == nil {
		t.Fatal("unbounded range accepted")
	}
	c.Targets = "192.0.2.1"
	c.SNI = "bad\r\nHost: injected"
	if _, e = prepare(c); e == nil {
		t.Fatal("header injection accepted")
	}
}
func TestTLSProbe(t *testing.T) {
	s := pipeServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS.ServerName != r.Host || r.URL.Path != "/cdn-cgi/trace" {
			t.Error("SNI/Host/path mismatch")
		}
		w.Header().Set("Server", "cloudflare")
		fmt.Fprintf(w, "fl=123\ncolo=LAX\nloc=US\nh=%s\nip=203.0.113.4\n", r.Host)
	}))
	defer s.Close()
	host, port, _ := net.SplitHostPort(s.Listener.Addr().String())
	p, _ := strconv.Atoi(port)
	c := defaults()
	c.Insecure = true
	o := observeWithDial(context.Background(), Target{host, p}, c.SNI, c, s.pipe.dial)
	if o.Error != "" || !o.CF || !o.Trace || o.TTFB <= 0 {
		t.Fatalf("%+v", o)
	}
	c.Insecure = false
	o = observeWithDial(context.Background(), Target{host, p}, c.SNI, c, s.pipe.dial)
	if o.Error == "" {
		t.Fatal("untrusted certificate accepted")
	}
}
func TestNoRedirectEscape(t *testing.T) {
	calls := 0
	s := pipeServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Redirect(w, r, "https://other.example/", 302)
	}))
	defer s.Close()
	h, p, _ := net.SplitHostPort(s.Listener.Addr().String())
	n, _ := strconv.Atoi(p)
	c := defaults()
	c.Insecure = true
	o := observeWithDial(context.Background(), Target{h, n}, c.SNI, c, s.pipe.dial)
	if calls != 1 || o.Status != 302 || o.CF {
		t.Fatalf("%d %+v", calls, o)
	}
}

type pipeListener struct {
	conns chan net.Conn
	done  chan struct{}
	once  sync.Once
}

func (p *pipeListener) Accept() (net.Conn, error) {
	select {
	case c := <-p.conns:
		return c, nil
	case <-p.done:
		return nil, net.ErrClosed
	}
}
func (p *pipeListener) Close() error   { p.once.Do(func() { close(p.done) }); return nil }
func (p *pipeListener) Addr() net.Addr { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 443} }
func (p *pipeListener) dial(ctx context.Context, _, _ string) (net.Conn, error) {
	a, b := net.Pipe()
	select {
	case p.conns <- b:
		return a, nil
	case <-ctx.Done():
		a.Close()
		b.Close()
		return nil, ctx.Err()
	}
}

type memoryServer struct {
	*httptest.Server
	pipe *pipeListener
}

func pipeServer(h http.Handler) *memoryServer {
	s := &httptest.Server{Listener: &pipeListener{conns: make(chan net.Conn), done: make(chan struct{})}, Config: &http.Server{Handler: h}}
	p := s.Listener.(*pipeListener)
	s.StartTLS()
	return &memoryServer{s, p}
}

// Concurrent workers must not interleave JSON/CSV writes or UI progress callbacks.
func TestScanSerializesResults(t *testing.T) {
	c := defaults()
	c.Concurrency = 8
	c.Rate = 2000
	c.Rounds = 1
	c.Timeout = 100
	targets := make([]Target, 16)
	for i := range targets {
		targets[i] = Target{"127.0.0.1", 1}
	}
	active, count := 0, 0
	scan(context.Background(), c, targets, func(r Result) {
		active++
		if active != 1 {
			t.Error("concurrent result callback")
		}
		time.Sleep(time.Millisecond)
		count++
		active--
	})
	if count != len(targets) {
		t.Fatalf("got %d results", count)
	}
}
