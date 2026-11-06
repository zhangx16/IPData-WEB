package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

const fixtureUUID = "00000000-0000-4000-8000-000000000001"

func fixtureTemplate(transport string) string {
	return "vless://" + fixtureUUID + "@192.0.2.9:9999?encryption=none&security=tls&sni=example.com&host=ws.example&type=" + transport + "&path=" + url.QueryEscape("/tunnel?x=1")
}
func TestTemplateValidation(t *testing.T) {
	n, e := parseNodeTemplate(fixtureTemplate("ws"))
	if e != nil || n.SNI != "example.com" || n.Host != "ws.example" || n.Path != "/tunnel?x=1" {
		t.Fatalf("%+v %v", n, e)
	}
	for _, raw := range []string{strings.Replace(fixtureTemplate("ws"), "security=tls", "security=reality", 1), fixtureTemplate("grpc"), fixtureTemplate("ws") + "&flow=xtls-rprx-vision", fixtureTemplate("ws") + "&sni=second.example", strings.Replace(fixtureTemplate("ws"), fixtureUUID, "bad", 1), fixtureTemplate("ws") + "&fp=chrome", strings.Replace(fixtureTemplate("ws"), "host=ws.example", "host=evil%0d%0aX-Injected%3Ayes", 1)} {
		_, e := parseNodeTemplate(raw)
		if e == nil {
			t.Errorf("bad template accepted")
		}
		if e != nil && strings.Contains(e.Error(), fixtureUUID) {
			t.Error("parser leaked credentials")
		}
	}
}

type nodeFixture struct {
	target       Target
	roots        *x509.CertPool
	url          string
	requests     atomic.Int32
	downloads    atomic.Int32
	wrongVersion atomic.Bool
	badUUID      atomic.Bool
}

func makeNodeFixture(t *testing.T, transport string) *nodeFixture {
	t.Helper()
	f := &nodeFixture{}
	backend := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.requests.Add(1)
		switch r.URL.Path {
		case "/cdn-cgi/trace":
			host, _, e := net.SplitHostPort(r.Host)
			if e != nil {
				host = r.Host
			}
			fmt.Fprintf(w, "fl=123\ncolo=LAX\nh=%s\nip=203.0.113.25\n", host)
		case "/download":
			f.downloads.Add(1)
			w.Header().Set("Content-Type", "application/octet-stream")
			for i := 0; i < 8; i++ {
				_, _ = w.Write(make([]byte, 4096))
				w.(http.Flusher).Flush()
				time.Sleep(time.Millisecond)
			}
		case "/hang":
			<-r.Context().Done()
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(backend.Close)
	f.url = backend.URL
	f.roots = x509.NewCertPool()
	f.roots.AddCert(backend.Certificate())
	n, _ := parseNodeTemplate(fixtureTemplate(transport))
	var active sync.Map
	bridge := func(conn net.Conn) {
		active.Store(conn, true)
		defer active.Delete(conn)
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		var prefix [18]byte
		if _, e := io.ReadFull(conn, prefix[:]); e != nil {
			return
		}
		if prefix[0] != 0 || !bytes.Equal(prefix[1:17], n.UUID[:]) || f.badUUID.Load() {
			return
		}
		if _, e := io.CopyN(io.Discard, conn, int64(prefix[17])); e != nil {
			return
		}
		var cmd [4]byte
		if _, e := io.ReadFull(conn, cmd[:]); e != nil || cmd[0] != 1 {
			return
		}
		port := binary.BigEndian.Uint16(cmd[1:3])
		var host string
		switch cmd[3] {
		case 1:
			var ip [4]byte
			if _, e := io.ReadFull(conn, ip[:]); e != nil {
				return
			}
			host = net.IP(ip[:]).String()
		case 3:
			var ip [16]byte
			if _, e := io.ReadFull(conn, ip[:]); e != nil {
				return
			}
			host = net.IP(ip[:]).String()
		case 2:
			var length [1]byte
			if _, e := io.ReadFull(conn, length[:]); e != nil {
				return
			}
			b := make([]byte, int(length[0]))
			if _, e := io.ReadFull(conn, b); e != nil {
				return
			}
			host = string(b)
		default:
			return
		}
		expected := strings.TrimPrefix(backend.URL, "https://")
		if net.JoinHostPort(host, fmt.Sprint(port)) != expected {
			t.Errorf("unexpected VLESS destination %s:%d", host, port)
			return
		}
		if f.wrongVersion.Load() {
			_, _ = conn.Write([]byte{1, 0})
			return
		}
		up, e := net.Dial("tcp", expected)
		if e != nil {
			t.Error(e)
			return
		}
		defer up.Close()
		if _, e = conn.Write([]byte{0, 0}); e != nil {
			return
		}
		go func() { _, _ = io.Copy(up, conn); up.Close() }()
		_, _ = io.Copy(conn, up)
	}
	if transport == "ws" {
		up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.TLS.ServerName != "example.com" || r.Host != "ws.example" || r.URL.RequestURI() != "/tunnel?x=1" {
				t.Error("template SNI/Host/path not preserved")
				http.Error(w, "bad template routing", 400)
				return
			}
			ws, e := up.Upgrade(w, r, nil)
			if e != nil {
				return
			}
			bridge(&wsStream{Conn: ws})
		}))
		t.Cleanup(server.Close)
		f.roots.AddCert(server.Certificate())
		addr := server.Listener.Addr().(*net.TCPAddr)
		f.target = Target{addr.IP.String(), addr.Port}
	} else {
		listener, e := tls.Listen("tcp", "127.0.0.1:0", backend.TLS.Clone())
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { listener.Close() })
		addr := listener.Addr().(*net.TCPAddr)
		f.target = Target{addr.IP.String(), addr.Port}
		go func() {
			for {
				conn, e := listener.Accept()
				if e != nil {
					return
				}
				go bridge(conn)
			}
		}()
	}
	t.Cleanup(func() { active.Range(func(k, v any) bool { k.(net.Conn).Close(); return true }) })
	return f
}
func TestNativeVLESS(t *testing.T) {
	for _, transport := range []string{"ws", "tcp"} {
		t.Run(transport, func(t *testing.T) {
			f := makeNodeFixture(t, transport)
			n, _ := parseNodeTemplate(fixtureTemplate(transport))
			c := defaults()
			c.Mode = "vless"
			c.NodeCheckURL = f.url + "/cdn-cgi/trace"
			c.NodeTimeout = 2000
			o := probeNodeWithRoots(context.Background(), c, f.target, n, f.roots)
			if o.Error != "" || o.Status != 200 || o.ExitIP != "203.0.113.25" || o.TTFB <= 0 {
				t.Fatalf("%+v", o)
			}
			c.SpeedURL = f.url + "/download"
			c.SpeedBytes = 16384
			s := nodeSpeedWithRoots(context.Background(), c, f.target, n, f.roots)
			if s.Error != "" || s.Bytes != 16384 || s.Mbps <= 0 || s.Via != "vless" {
				t.Fatalf("%+v", s)
			}
			f.badUUID.Store(true)
			before := f.requests.Load()
			o = probeNodeWithRoots(context.Background(), c, f.target, n, f.roots)
			if o.Error == "" || f.requests.Load() != before {
				t.Fatal("invalid UUID accepted or fallback dialled destination")
			}
			f.badUUID.Store(false)
			f.wrongVersion.Store(true)
			o = probeNodeWithRoots(context.Background(), c, f.target, n, f.roots)
			if o.Error == "" {
				t.Fatal("invalid VLESS response accepted")
			}
			f.wrongVersion.Store(false)
			c.Insecure = true
			o = probeNodeWithRoots(context.Background(), c, f.target, n, nil)
			if o.Error == "" {
				t.Fatal("outer insecure flag bypassed inner HTTPS certificate verification")
			}
		})
	}
}
func TestVLESSCancellation(t *testing.T) {
	f := makeNodeFixture(t, "ws")
	n, _ := parseNodeTemplate(fixtureTemplate("ws"))
	c := defaults()
	c.NodeCheckURL = f.url + "/hang"
	c.NodeTimeout = 2000
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	start := time.Now()
	o := probeNodeWithRoots(ctx, c, f.target, n, f.roots)
	if o.Error == "" || time.Since(start) > time.Second {
		t.Fatalf("cancel failed %+v", o)
	}
}
func TestNodeSpeedOptional(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			c := defaults()
			c.Mode = "vless"
			c.NodeTemplate = fixtureTemplate("ws")
			c.SpeedEnabled = &enabled
			c.SpeedURL = "https://speed.example/download"
			c.Targets = "192.0.2.1,192.0.2.2"
			c.Ports = "443"
			c.Rounds = 2
			c.Rate = 2000
			targets, e := prepare(c)
			if e != nil {
				t.Fatal(e)
			}
			var checks, downloads atomic.Int32
			var results []Result
			scanWithNodeChecks(context.Background(), c, targets, func(r Result) { results = append(results, r) }, func(context.Context, Config, Target, *nodeTemplate) NodeObservation {
				checks.Add(1)
				return NodeObservation{Status: 200, TTFB: 12}
			}, func(context.Context, Config, Target, *nodeTemplate) SpeedResult {
				downloads.Add(1)
				return SpeedResult{Via: "vless", Bytes: 1024, Mbps: 1}
			})
			if checks.Load() != 4 || downloads.Load() != map[bool]int32{true: 2, false: 0}[enabled] || len(results) != 2 {
				t.Fatalf("checks%d downloads%d results%d", checks.Load(), downloads.Load(), len(results))
			}
			for _, r := range results {
				if !resultHit(r) || r.Node.Success != 2 || (r.Speed != nil) != enabled {
					t.Fatal("bad node result")
				}
			}
		})
	}
}
func TestTemplatePersistenceRedacted(t *testing.T) {
	dir := t.TempDir()
	m, e := newManager(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer m.close()
	c := defaults()
	c.Mode = "vless"
	c.NodeTemplate = fixtureTemplate("ws")
	c.Targets = "192.0.2.1"
	c.Ports = "443"
	c.Rounds = 1
	disabled := false
	c.SpeedEnabled = &disabled
	c.SpeedURL = "not-a-url"
	created, e := m.create("private template", c)
	if e != nil {
		t.Fatal(e)
	}
	assertHidden := func(v any) {
		t.Helper()
		b, e := json.Marshal(v)
		if e != nil {
			t.Fatal(e)
		}
		if strings.Contains(string(b), fixtureUUID) || strings.Contains(string(b), "/tunnel") {
			t.Fatal("template leaked in public response")
		}
	}
	assertHidden(created)
	assertHidden(m.list())
	get, _, _ := m.get(created.ID, 0, 10)
	assertHidden(get)
	if !get.HasTemplate {
		t.Fatal("saved template marker missing")
	}
	m.close()
	restored, e := newManager(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer restored.close()
	if restored.tasks[created.ID].Config.NodeTemplate != c.NodeTemplate {
		t.Fatal("resume lost template")
	}
	b, e := os.ReadFile(filepath.Join(dir, created.ID+".json"))
	if e != nil || !strings.Contains(string(b), fixtureUUID) {
		t.Fatal("local template missing")
	}
	restored.runner = func(ctx context.Context, config Config, targets []Target, emit func(Result)) {
		if config.NodeTemplate != c.NodeTemplate || wantSpeed(config) {
			t.Error("resume changed private template or download choice")
		}
		emit(Result{IP: targets[0].IP, Port: targets[0].Port, Evidence: "not_run", Rounds: 1, Node: &NodeResult{Success: 1, Rounds: 1, Median: 10, Observations: []NodeObservation{{Status: 200, TTFB: 10}}}})
	}
	if e = restored.start(created.ID); e != nil {
		t.Fatal(e)
	}
	finished := awaitTask(t, restored, created.ID, func(v Task) bool { return v.Status == "completed" })
	if finished.Hits != 1 {
		t.Fatal("node-only pass was not counted as hit")
	}
	restored.close()
	again, e := newManager(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer again.close()
	loaded, _, _ := again.get(created.ID, 0, 10)
	if loaded.Hits != 1 || loaded.Completed != 1 {
		t.Fatal("node hits not restored from journal")
	}
	assertHidden(loaded)
}
