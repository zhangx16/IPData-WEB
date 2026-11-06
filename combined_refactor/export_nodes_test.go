package main

import (
	"bytes"
	"encoding/json"
	"math"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

const exportFixture = "vless://00000000-0000-4000-8000-000000000001@old.example:443?encryption=none&security=tls&type=ws&sni=tls.example&host=ws.example&path=%2Ftunnel%3Fx%3D1&custom=a%2Bb#%E7%BE%8E%E5%9B%BD"

func exportRows() []Result {
	return []Result{
		{IP: "192.0.2.1", Port: 8443, Evidence: "dual_verified", Success: 2, Rounds: 2, Median: 12, Node: &NodeResult{Success: 2, Rounds: 2, Median: 20}, Speed: &SpeedResult{Bytes: 1024, Mbps: 10}},
		{IP: "2001:db8::99", Port: 443, Evidence: "trace_valid", Success: 2, Rounds: 2, Median: 30, Speed: &SpeedResult{Bytes: 1024, Mbps: 5}},
		{IP: "192.0.2.3", Port: 443, Speed: &SpeedResult{Bytes: 512, Mbps: 2, Error: "short read"}},
		{IP: "192.0.2.4", Port: 443, Speed: &SpeedResult{Bytes: 0, Mbps: 2}},
		{IP: "192.0.2.5", Port: 443},
	}
}
func TestVLESSLinkExportPreservesParameters(t *testing.T) {
	base, e := parseExportTemplate(exportFixture)
	if e != nil {
		t.Fatal(e)
	}
	candidates := collectNodeCandidates(exportRows(), nodeExportRequest{Scope: "all"})
	var b bytes.Buffer
	if e = writeNodeLinks(&b, base, candidates, false); e != nil {
		t.Fatal(e)
	}
	lines := strings.Fields(b.String())
	if len(lines) != 2 {
		t.Fatal(len(lines))
	}
	for i, line := range lines {
		u, e := url.Parse(line)
		if e != nil {
			t.Fatal(e)
		}
		if u.RawQuery != base.RawQuery || u.User.String() != base.User.String() || u.EscapedFragment() != base.EscapedFragment() {
			t.Fatal("template fields changed")
		}
		if u.Hostname() != candidates[i].Target.IP {
			t.Fatal("wrong candidate IP")
		}
		if i == 0 && u.Port() != "8443" {
			t.Fatal("candidate port not used")
		}
		if i == 1 && !strings.Contains(u.Host, "[2001:db8::99]") {
			t.Fatal("IPv6 brackets missing")
		}
	}
	b.Reset()
	_ = writeNodeLinks(&b, base, candidates, true)
	for _, line := range strings.Split(strings.TrimSpace(b.String()), "\n") {
		u, _ := url.Parse(line)
		if !strings.Contains(u.Fragment, u.Host) {
			t.Fatal("address absent from remark")
		}
	}
}
func TestNodeExportFiltersAndSelection(t *testing.T) {
	rows := exportRows()
	rows = append(rows, rows[0])
	q := nodeExportRequest{Scope: "selected", Selected: []nodeExportTarget{{"192.0.2.1", 8443}, {"192.0.2.1", 8443}, {"192.0.2.3", 443}, {"192.0.2.99", 443}}}
	got := collectNodeCandidates(rows, q)
	if len(got) != 1 || got[0].Target.IP != "192.0.2.1" {
		t.Fatalf("%+v", got)
	}
	max := 25.0
	got = collectNodeCandidates(rows, nodeExportRequest{Scope: "filtered", Filter: "node_ok", MaxLatency: &max, MinSuccess: 100})
	if len(got) != 1 {
		t.Fatal(len(got))
	}
	got = collectNodeCandidates(rows, nodeExportRequest{Scope: "all", Filter: "node_failed", MaxLatency: &max})
	if len(got) != 2 {
		t.Fatal("all scope applied active filters")
	}
	for _, speed := range []float64{0, -1, math.NaN(), math.Inf(1)} {
		if downloadSucceeded(Result{Speed: &SpeedResult{Bytes: 1, Mbps: speed}}) {
			t.Fatal("invalid download marked eligible")
		}
	}
}
func TestNodeExportAPI(t *testing.T) {
	m, e := newManager(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer m.close()
	c := defaults()
	c.Mode = "vless"
	c.NodeTemplate = fixtureTemplate("ws")
	c.Targets = "192.0.2.1,2001:db8::99"
	c.Ports = "443,8443"
	task, e := m.create("export", c)
	if e != nil {
		t.Fatal(e)
	}
	m.tasks[task.ID].Results = exportRows()
	handler := api(m)
	post := func(q nodeExportRequest) *httptest.ResponseRecorder {
		b, _ := json.Marshal(q)
		r := httptest.NewRequest("POST", "/api/tasks/"+task.ID+"/export-vless", bytes.NewReader(b))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	w := post(nodeExportRequest{Scope: "all", Preview: true})
	if w.Code != 200 || strings.Contains(w.Body.String(), fixtureUUID) || !strings.Contains(w.Body.String(), `"count":2`) {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	w = post(nodeExportRequest{Scope: "all", TemplateSource: "saved"})
	if w.Code != 200 || w.Header().Get("X-IPData-Node-Count") != "2" || !strings.Contains(w.Body.String(), fixtureUUID) || !strings.Contains(w.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	original := m.tasks[task.ID].Config.NodeTemplate
	w = post(nodeExportRequest{Scope: "all", TemplateSource: "custom", Template: exportFixture})
	if w.Code != 200 || m.tasks[task.ID].Config.NodeTemplate != original {
		t.Fatal("custom export mutated saved task")
	}
	w = post(nodeExportRequest{Scope: "selected", TemplateSource: "saved", Selected: []nodeExportTarget{{"192.0.2.3", 443}}})
	if w.Code != 400 {
		t.Fatal("failed download exported")
	}
	for _, bad := range []string{"vless://bad-secret@old.example:443?security=tls", "https://user:secret@host.example/", "vless://%zz"} {
		w = post(nodeExportRequest{Scope: "all", TemplateSource: "custom", Template: bad})
		if w.Code != 400 || strings.Contains(w.Body.String(), "bad-secret") {
			t.Fatal("bad template accepted/leaked")
		}
	}
	req := httptest.NewRequest("GET", "/api/tasks/"+task.ID+"/export-vless", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 405 {
		t.Fatal("credential export accepts GET")
	}
}
