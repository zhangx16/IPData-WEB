package main

import (
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"net"
	"net/netip"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

type nodeExportTarget struct {
	IP   string `json:"ip"`
	Port int    `json:"port"`
}
type nodeExportRequest struct {
	Scope          string             `json:"scope"`
	TemplateSource string             `json:"template_source"`
	Template       string             `json:"template"`
	Selected       []nodeExportTarget `json:"selected"`
	Filter         string             `json:"filter"`
	MaxLatency     *float64           `json:"max_latency_ms"`
	MinSuccess     float64            `json:"min_success"`
	Sort           string             `json:"sort"`
	Rename         *bool              `json:"append_address"`
	Preview        bool               `json:"preview"`
}
type nodeExportCandidate struct {
	Target                  nodeExportTarget
	Quality, Latency, Speed float64
}

func downloadSucceeded(r Result) bool {
	return r.Speed != nil && r.Speed.Error == "" && r.Speed.Bytes > 0 && r.Speed.Mbps > 0 && !math.IsNaN(r.Speed.Mbps) && !math.IsInf(r.Speed.Mbps, 0)
}
func resultQuality(r Result) (float64, float64) {
	success, rounds, delay := r.Success, r.Rounds, r.Median
	if r.Node != nil {
		success, rounds, delay = r.Node.Success, r.Node.Rounds, r.Node.Median
	}
	if rounds <= 0 {
		return 0, delay
	}
	return float64(success) / float64(rounds), delay
}
func exportTargetKey(ip string, port int) (string, bool) {
	a, e := netip.ParseAddr(ip)
	if e != nil || a.Zone() != "" || port < 1 || port > 65535 {
		return "", false
	}
	return net.JoinHostPort(a.Unmap().String(), strconv.Itoa(port)), true
}
func validExportRequest(q nodeExportRequest) error {
	switch q.Scope {
	case "", "filtered", "all", "selected":
	default:
		return fmt.Errorf("导出范围无效")
	}
	switch q.TemplateSource {
	case "", "saved", "custom":
	default:
		return fmt.Errorf("模板来源无效")
	}
	switch q.Filter {
	case "", "all", "hits", "dual_verified", "trace_valid", "cf_headers", "node_ok", "node_failed", "download_ok":
	default:
		return fmt.Errorf("结果筛选无效")
	}
	if math.IsNaN(q.MinSuccess) || math.IsInf(q.MinSuccess, 0) || q.MinSuccess < 0 || q.MinSuccess > 100 {
		return fmt.Errorf("最低成功率须为0–100")
	}
	if q.MaxLatency != nil && (*q.MaxLatency < 0 || math.IsNaN(*q.MaxLatency) || math.IsInf(*q.MaxLatency, 0)) {
		return fmt.Errorf("延迟上限无效")
	}
	if q.Sort != "" && q.Sort != "latency" && q.Sort != "speed" {
		return fmt.Errorf("排序方式无效")
	}
	return nil
}
func exportMatches(r Result, q nodeExportRequest) bool {
	quality, delay := resultQuality(r)
	if quality*100 < q.MinSuccess {
		return false
	}
	if q.MaxLatency != nil && (!resultHit(r) || delay > *q.MaxLatency) {
		return false
	}
	switch q.Filter {
	case "", "all":
		return true
	case "hits":
		return resultHit(r)
	case "node_ok":
		return r.Node != nil && r.Node.Success > 0 && r.Node.Success == r.Node.Rounds
	case "node_failed":
		return r.Node != nil && r.Node.Success == 0
	case "download_ok":
		return downloadSucceeded(r)
	default:
		return r.Evidence == q.Filter
	}
}
func collectNodeCandidates(results []Result, q nodeExportRequest) []nodeExportCandidate {
	selected := map[string]bool{}
	for _, t := range q.Selected {
		if k, ok := exportTargetKey(t.IP, t.Port); ok {
			selected[k] = true
		}
	}
	seen := map[string]bool{}
	out := []nodeExportCandidate{}
	for _, r := range results {
		if !downloadSucceeded(r) {
			continue
		}
		k, ok := exportTargetKey(r.IP, r.Port)
		if !ok || seen[k] {
			continue
		}
		if q.Scope == "selected" && !selected[k] {
			continue
		}
		if (q.Scope == "" || q.Scope == "filtered") && !exportMatches(r, q) {
			continue
		}
		seen[k] = true
		ip, _ := netip.ParseAddr(r.IP)
		quality, delay := resultQuality(r)
		out = append(out, nodeExportCandidate{Target: nodeExportTarget{ip.Unmap().String(), r.Port}, Quality: quality, Latency: delay, Speed: r.Speed.Mbps})
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if q.Sort == "speed" && a.Speed != b.Speed {
			return a.Speed > b.Speed
		}
		if q.Sort != "speed" && a.Quality != b.Quality {
			return a.Quality > b.Quality
		}
		return a.Latency < b.Latency
	})
	return out
}
func parseExportTemplate(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(strings.TrimPrefix(raw, "\ufeff"))
	if len(raw) > 8192 {
		return nil, fmt.Errorf("VLESS模板过长")
	}
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "vless" || u.User == nil || u.Hostname() == "" || u.Opaque != "" {
		return nil, fmt.Errorf("请提供有效的vless://分享链接")
	}
	if _, ok := u.User.Password(); ok {
		return nil, fmt.Errorf("VLESS模板用户信息无效")
	}
	id := u.User.Username()
	b, e := hex.DecodeString(strings.ReplaceAll(id, "-", ""))
	if e != nil || len(b) != 16 || (len(id) != 32 && (len(id) != 36 || id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-')) {
		return nil, fmt.Errorf("VLESS模板UUID无效")
	}
	port, e := strconv.Atoi(u.Port())
	if e != nil || port < 1 || port > 65535 {
		return nil, fmt.Errorf("VLESS模板端口无效")
	}
	if _, e = url.ParseQuery(u.RawQuery); e != nil {
		return nil, fmt.Errorf("VLESS模板查询编码无效")
	}
	return u, nil
}
func writeNodeLinks(w io.Writer, base *url.URL, candidates []nodeExportCandidate, appendAddress bool) error {
	for _, candidate := range candidates {
		u := *base
		u.Host = net.JoinHostPort(candidate.Target.IP, strconv.Itoa(candidate.Target.Port))
		if appendAddress {
			label := base.Fragment
			if label == "" {
				label = "VLESS"
			}
			u.Fragment = label + " | " + u.Host
			u.RawFragment = ""
		}
		if _, e := io.WriteString(w, u.String()+"\n"); e != nil {
			return e
		}
	}
	return nil
}
