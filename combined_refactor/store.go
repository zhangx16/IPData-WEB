package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Task struct {
	HasTemplate bool     `json:"has_vless_template,omitempty"`
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Config      Config   `json:"config"`
	Status      string   `json:"status"`
	Created     string   `json:"created"`
	Updated     string   `json:"updated"`
	Total       int      `json:"total"`
	Completed   int      `json:"completed"`
	Hits        int      `json:"hits"`
	Error       string   `json:"error,omitempty"`
	Results     []Result `json:"-"`
}
type taskManager struct {
	mu     sync.Mutex
	dir    string
	tasks  map[string]*Task
	active string
	cancel context.CancelFunc
	wg     sync.WaitGroup
	closed bool
	unlock func()
	runner func(context.Context, Config, []Target, func(Result))
}

func newManager(dir string) (*taskManager, error) {
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	unlock, e := lockData(dir)
	if e != nil {
		return nil, e
	}
	success := false
	defer func() {
		if !success {
			unlock()
		}
	}()
	m := &taskManager{dir: dir, tasks: map[string]*Task{}, unlock: unlock}
	paths, e := filepath.Glob(filepath.Join(dir, "*.json"))
	if e != nil {
		return nil, e
	}
	for _, p := range paths {
		var t Task
		b, e := os.ReadFile(p)
		if e != nil {
			return nil, e
		}
		if e = json.Unmarshal(b, &t); e != nil {
			return nil, fmt.Errorf("任务清单%s: %w", p, e)
		}
		if !validID(t.ID) || filepath.Base(p) != t.ID+".json" {
			return nil, fmt.Errorf("任务ID无效%s", p)
		}
		if e = loadJournal(filepath.Join(dir, t.ID+".jsonl"), &t); e != nil {
			return nil, fmt.Errorf("任务%s: %w", t.ID, e)
		}
		if t.Status == "running" || t.Status == "pausing" || t.Status == "completed" && t.Completed != t.Total {
			t.Status = "paused"
			t.Error = "程序中断，已保留完成记录，可继续扫描"
		}
		m.tasks[t.ID] = &t
		if e = m.saveLocked(&t); e != nil {
			return nil, e
		}
	}
	success = true
	return m, nil
}
func validID(id string) bool {
	b, e := hex.DecodeString(id)
	return e == nil && len(b) == 12 && id == strings.ToLower(id)
}
func key(ip string, port int) string { return fmt.Sprintf("%s:%d", ip, port) }
func loadJournal(path string, t *Task) error {
	f, e := os.OpenFile(path, os.O_RDWR, 0600)
	if errors.Is(e, os.ErrNotExist) {
		if t.Completed != 0 {
			return fmt.Errorf("任务已有完成记录，但结果日志缺失")
		}
		return nil
	}
	if e != nil {
		return e
	}
	defer f.Close()
	r := bufio.NewReader(f)
	var offset int64
	seen := map[string]bool{}
	for {
		line, e := r.ReadBytes('\n')
		if e == io.EOF {
			if len(line) > 0 {
				if e = f.Truncate(offset); e != nil {
					return e
				}
				if e = f.Sync(); e != nil {
					return e
				}
			}
			break
		}
		if e != nil {
			return e
		}
		var result Result
		if e = json.Unmarshal(line, &result); e != nil {
			return fmt.Errorf("结果日志在字节%d损坏: %w", offset, e)
		}
		offset += int64(len(line))
		k := key(result.IP, result.Port)
		if seen[k] {
			return fmt.Errorf("结果日志存在重复目标%s", k)
		}
		seen[k] = true
		t.Results = append(t.Results, result)
	}
	t.Completed = len(t.Results)
	t.Hits = 0
	for _, r := range t.Results {
		if resultHit(r) {
			t.Hits++
		}
	}
	return nil
}
func (m *taskManager) saveLocked(t *Task) error {
	t.Updated = time.Now().UTC().Format(time.RFC3339Nano)
	b, e := json.MarshalIndent(t, "", "  ")
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(m.dir, ".task-*.tmp")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, e = f.Write(b); e != nil {
		f.Close()
		return e
	}
	if e = f.Sync(); e != nil {
		f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	if e = os.Rename(tmp, filepath.Join(m.dir, t.ID+".json")); e != nil {
		return e
	}
	if d, e := os.Open(m.dir); e == nil {
		_ = d.Sync()
		d.Close()
	}
	return nil
}
func (m *taskManager) create(name string, c Config) (Task, error) {
	if scanMode(c) == "cf" {
		c.NodeTemplate = ""
	}
	plan, e := planTargets(c)
	if e != nil {
		return Task{}, e
	}
	if len([]rune(name)) > 100 {
		return Task{}, fmt.Errorf("任务名最多100字")
	}
	b := make([]byte, 12)
	if _, e = rand.Read(b); e != nil {
		return Task{}, e
	}
	t := &Task{ID: hex.EncodeToString(b), Name: strings.TrimSpace(name), Config: c, Status: "paused", Created: time.Now().UTC().Format(time.RFC3339Nano), Total: plan.Count, Results: []Result{}}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return Task{}, fmt.Errorf("服务正在关闭")
	}
	if e = m.saveLocked(t); e != nil {
		return Task{}, e
	}
	m.tasks[t.ID] = t
	return taskCopy(t), nil
}
func taskCopy(t *Task) Task {
	v := *t
	v.Results = nil
	v.HasTemplate = t.Config.NodeTemplate != ""
	v.Config.NodeTemplate = ""
	return v
}
func (m *taskManager) list() []Task {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Task{}
	for _, t := range m.tasks {
		out = append(out, taskCopy(t))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created > out[j].Created })
	return out
}
func (m *taskManager) get(id string, offset, limit int) (Task, []Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.tasks[id]
	if t == nil {
		return Task{}, nil, os.ErrNotExist
	}
	if offset < 0 || limit < 1 || limit > 1000 {
		return Task{}, nil, fmt.Errorf("分页范围无效")
	}
	if offset > len(t.Results) {
		offset = len(t.Results)
	}
	end := min(offset+limit, len(t.Results))
	out := append([]Result{}, t.Results[offset:end]...)
	return taskCopy(t), out, nil
}
func (m *taskManager) start(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return fmt.Errorf("服务正在关闭")
	}
	t := m.tasks[id]
	if t == nil {
		return os.ErrNotExist
	}
	if m.active != "" {
		return fmt.Errorf("已有运行任务%s，请先暂停", m.active)
	}
	if t.Status == "completed" {
		return fmt.Errorf("任务已完成")
	}
	plan, e := planTargets(t.Config)
	if e != nil {
		return e
	}
	seen := map[string]bool{}
	for _, r := range t.Results {
		seen[key(r.IP, r.Port)] = true
	}
	override := m.runner
	var pending []Target
	if override != nil {
		plan.each(func(v Target) bool {
			if !seen[key(v.IP, v.Port)] {
				pending = append(pending, v)
			}
			return true
		})
	}
	f, e := os.OpenFile(filepath.Join(m.dir, id+".jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	t.Status = "running"
	t.Error = ""
	if e = m.saveLocked(t); e != nil {
		f.Close()
		t.Status = "failed"
		return e
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.active = id
	m.cancel = cancel
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer cancel()
		var failure error
		emit := func(r Result) {
			m.mu.Lock()
			defer m.mu.Unlock()
			if failure != nil {
				return
			}
			b, e := json.Marshal(r)
			if e == nil {
				_, e = f.Write(append(b, '\n'))
			}
			if e == nil {
				e = f.Sync()
			}
			if e != nil {
				failure = e
				cancel()
				return
			}
			t.Results = append(t.Results, r)
			t.Completed++
			if resultHit(r) {
				t.Hits++
			}
			t.Updated = time.Now().UTC().Format(time.RFC3339Nano)
		}
		if override != nil {
			override(ctx, t.Config, pending, emit)
		} else {
			scanTargetPlan(ctx, t.Config, plan, seen, emit)
		}
		closeErr := f.Close()
		m.mu.Lock()
		defer m.mu.Unlock()
		if failure == nil {
			failure = closeErr
		}
		switch {
		case failure != nil:
			t.Status = "failed"
			t.Error = failure.Error()
		case t.Completed == t.Total:
			t.Status = "completed"
		case ctx.Err() != nil:
			t.Status = "paused"
		default:
			t.Status = "failed"
			t.Error = "扫描提前结束"
		}
		if e := m.saveLocked(t); e != nil {
			t.Status = "failed"
			t.Error = e.Error()
		}
		m.active = ""
		m.cancel = nil
	}()
	return nil
}
func (m *taskManager) pause(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.tasks[id]
	if t == nil {
		return os.ErrNotExist
	}
	if m.active != id {
		return nil
	}
	t.Status = "pausing"
	m.cancel()
	return m.saveLocked(t)
}
func (m *taskManager) close() {
	m.mu.Lock()
	m.closed = true
	if m.cancel != nil {
		m.cancel()
	}
	m.mu.Unlock()
	m.wg.Wait()
	m.mu.Lock()
	if m.unlock != nil {
		m.unlock()
		m.unlock = nil
	}
	m.mu.Unlock()
}
