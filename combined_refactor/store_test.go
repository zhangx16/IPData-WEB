package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func awaitTask(t *testing.T, m *taskManager, id string, condition func(Task) bool) Task {
	t.Helper()
	end := time.Now().Add(10 * time.Second)
	for time.Now().Before(end) {
		v, _, e := m.get(id, 0, 1)
		if e != nil {
			t.Fatal(e)
		}
		if condition(v) {
			return v
		}
		time.Sleep(5 * time.Millisecond)
	}
	v, _, _ := m.get(id, 0, 1)
	t.Fatalf("task deadline: %+v", v)
	return Task{}
}
func TestPersistenceCrashRecovery(t *testing.T) {
	dir := t.TempDir()
	m, e := newManager(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer m.close()
	if other, e := newManager(dir); e == nil {
		other.close()
		t.Fatal("directory lock missing")
	}
	c := defaults()
	c.Targets = "192.0.2.1,192.0.2.2,192.0.2.3"
	c.Ports = "443"
	task, e := m.create("恢复测试", c)
	if e != nil {
		t.Fatal(e)
	}
	m.runner = func(ctx context.Context, c Config, ts []Target, emit func(Result)) {
		emit(Result{IP: ts[0].IP, Port: ts[0].Port, Success: 1, Rounds: 1, Evidence: "cf_headers"})
		<-ctx.Done()
	}
	if e = m.start(task.ID); e != nil {
		t.Fatal(e)
	}
	awaitTask(t, m, task.ID, func(v Task) bool { return v.Completed == 1 })
	m.close()
	path := filepath.Join(dir, task.ID+".json")
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	var manifest Task
	if e = json.Unmarshal(b, &manifest); e != nil {
		t.Fatal(e)
	}
	manifest.Status = "running"
	manifest.Completed = 0
	manifest.Hits = 0
	b, _ = json.Marshal(manifest)
	if e = os.WriteFile(path, b, 0600); e != nil {
		t.Fatal(e)
	}
	logPath := filepath.Join(dir, task.ID+".jsonl")
	f, e := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	_, _ = f.WriteString(`{"ip":"torn`)
	f.Close()
	restored, e := newManager(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer restored.close()
	v, rs, _ := restored.get(task.ID, 0, 100)
	if v.Status != "paused" || v.Completed != 1 || v.Hits != 1 || len(rs) != 1 {
		t.Fatalf("recovery %+v", v)
	}
	restored.runner = func(ctx context.Context, c Config, ts []Target, emit func(Result)) {
		if len(ts) != 2 {
			t.Errorf("expected2 pending got%d", len(ts))
		}
		for _, target := range ts {
			if target.IP == rs[0].IP {
				t.Error("completed target repeated")
			}
			emit(Result{IP: target.IP, Port: target.Port, Rounds: 1})
		}
	}
	if e = restored.start(task.ID); e != nil {
		t.Fatal(e)
	}
	awaitTask(t, restored, task.ID, func(v Task) bool { return v.Status == "completed" })
	_, all, _ := restored.get(task.ID, 0, 100)
	if len(all) != 3 {
		t.Fatal(len(all))
	}
}
func TestJournalCorruptionNotDiscarded(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.jsonl")
	if e := os.WriteFile(path, []byte("not-json\n"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := loadJournal(path, &Task{}); e == nil {
		t.Fatal("corrupt full row discarded")
	}
}
func TestAPI(t *testing.T) {
	m, e := newManager(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer m.close()
	m.runner = func(ctx context.Context, c Config, ts []Target, emit func(Result)) { <-ctx.Done() }
	handler := api(m)
	c := defaults()
	c.Targets = "127.0.0.1"
	body, _ := json.Marshal(map[string]any{"name": "API", "config": c})
	post := func(path, body, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if w := post("/api/tasks", string(body), "http://evil.example"); w.Code != 403 {
		t.Fatal(w.Code)
	}
	w := post("/api/tasks", string(body), "")
	if w.Code != 201 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	var task Task
	if e = json.Unmarshal(w.Body.Bytes(), &task); e != nil {
		t.Fatal(e)
	}
	path := "/api/tasks/" + task.ID
	if w = post(path+"/resume", "{}", ""); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w = post(path+"/resume", "{}", ""); w.Code != 409 {
		t.Fatal(w.Code)
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", path+"?offset=0&limit=1000", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "running") {
		t.Fatal(w.Body.String())
	}
	if w = post(path+"/pause", "{}", ""); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	awaitTask(t, m, task.ID, func(v Task) bool { return v.Status == "paused" })
	if w = post("/api/tasks", string(body)+"{}", ""); w.Code != 400 {
		t.Fatal("trailing JSON accepted")
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", path+"/export?format=csv", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "speed_mbps") {
		t.Fatal(w.Body.String())
	}
}
func TestLocalHTTPSPauseRestartSpeed(t *testing.T) {
	var mu sync.Mutex
	calls := map[string]int{}
	ports := []string{}
	servers := []*httptest.Server{}
	for i := 0; i < 6; i++ {
		s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.TLS.ServerName != r.Host {
				t.Error("SNI and Host mismatch")
			}
			mu.Lock()
			calls[r.TLS.ServerName+":"+r.Context().Value(http.LocalAddrContextKey).(interface{ String() string }).String()]++
			mu.Unlock()
			if r.URL.Path == "/download" {
				w.Header().Set("Content-Type", "application/octet-stream")
				for j := 0; j < 4; j++ {
					_, _ = w.Write(make([]byte, 8192))
					w.(http.Flusher).Flush()
					time.Sleep(20 * time.Millisecond)
				}
				return
			}
			time.Sleep(30 * time.Millisecond)
			w.Header().Set("Server", "cloudflare")
			fmt.Fprintf(w, "fl=123\ncolo=LAX\nloc=US\nh=%s\nip=203.0.113.10\n", r.Host)
		}))
		servers = append(servers, s)
		ports = append(ports, strconv.Itoa(s.Listener.Addr().(*net.TCPAddr).Port))
	}
	defer func() {
		for _, s := range servers {
			s.Close()
		}
	}()
	c := defaults()
	c.Targets = "127.0.0.1"
	c.Ports = strings.Join(ports, ",")
	c.SNI = "alpha.example"
	c.VerifySNI = "beta.example"
	c.Insecure = true
	c.Concurrency = 1
	c.Rate = 1000
	c.Rounds = 1
	c.SpeedURL = "https://download.example/download"
	c.SpeedBytes = 32768
	dir := t.TempDir()
	m, e := newManager(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer m.close()
	task, e := m.create("本地真实TLS集成测试", c)
	if e != nil {
		t.Fatal(e)
	}
	if e = m.start(task.ID); e != nil {
		t.Fatal(e)
	}
	awaitTask(t, m, task.ID, func(v Task) bool { return v.Completed >= 1 })
	if e = m.pause(task.ID); e != nil {
		t.Fatal(e)
	}
	paused := awaitTask(t, m, task.ID, func(v Task) bool { return v.Status == "paused" })
	if paused.Completed >= paused.Total {
		t.Fatal("pause did not interrupt task")
	}
	_, before, _ := m.get(task.ID, 0, 100)
	m.close()
	restored, e := newManager(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer restored.close()
	if e = restored.start(task.ID); e != nil {
		t.Fatal(e)
	}
	complete := awaitTask(t, restored, task.ID, func(v Task) bool { return v.Status == "completed" })
	_, results, _ := restored.get(task.ID, 0, 100)
	for _, r := range results {
		if r.Evidence != "dual_verified" || r.Speed == nil || r.Speed.Error != "" || r.Speed.Bytes != 32768 || r.Speed.Mbps <= 0 {
			t.Fatalf("invalid result: %+v", r)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	for _, r := range before {
		for _, sni := range []string{c.SNI, c.VerifySNI, "download.example"} {
			k := fmt.Sprintf("%s:127.0.0.1:%d", sni, r.Port)
			if calls[k] != 1 {
				t.Errorf("completed target replayed: %s count%d", k, calls[k])
			}
		}
	}
	t.Logf("paused at %d/%d; restored to %d/%d; all six dual verified with 32768-byte downloads", paused.Completed, paused.Total, complete.Completed, complete.Total)
	if p := os.Getenv("IPDATA_LOCAL_REPORT"); p != "" {
		b, _ := json.MarshalIndent(map[string]any{"scope": "localhost controlled TLS fixture, not DMIT", "paused": paused.Completed, "completed": complete.Completed, "results": results}, "", "  ")
		if e = os.WriteFile(p, b, 0600); e != nil {
			t.Fatal(e)
		}
	}
}
