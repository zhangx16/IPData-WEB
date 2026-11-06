package main

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
)

//go:embed dmit.json
var dmitData []byte

func jsonReply(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}
func api(m *taskManager) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, page)
	})
	mux.HandleFunc("/api/providers/dmit", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			http.Error(w, "GET required", 405)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(dmitData)
	})
	mux.HandleFunc("/api/tasks", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "GET":
			jsonReply(w, m.list())
		case "POST":
			var body struct {
				Name   string `json:"name"`
				Config Config `json:"config"`
			}
			if e := decodeBody(w, r, &body); e != nil {
				http.Error(w, e.Error(), 400)
				return
			}
			t, e := m.create(body.Name, body.Config)
			if e != nil {
				http.Error(w, e.Error(), 400)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(201)
			jsonReply(w, t)
		default:
			http.Error(w, "method not allowed", 405)
		}
	})
	mux.HandleFunc("/api/tasks/", func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/tasks/"), "/")
		if len(parts) > 2 || !validID(parts[0]) {
			http.NotFound(w, r)
			return
		}
		id := parts[0]
		action := ""
		if len(parts) == 2 {
			action = parts[1]
		}
		fail := func(e error) {
			status := 409
			if errors.Is(e, os.ErrNotExist) {
				status = 404
			}
			http.Error(w, e.Error(), status)
		}
		switch action {
		case "":
			if r.Method != "GET" {
				http.Error(w, "GET required", 405)
				return
			}
			offset, e := queryInt(r, "offset", 0)
			if e != nil {
				http.Error(w, e.Error(), 400)
				return
			}
			limit, e := queryInt(r, "limit", 500)
			if e != nil || limit < 1 || limit > 1000 || offset < 0 {
				http.Error(w, "invalid pagination", 400)
				return
			}
			t, rs, e := m.get(id, offset, limit)
			if e != nil {
				fail(e)
				return
			}
			jsonReply(w, map[string]any{"task": t, "results": rs, "next": min(offset+len(rs), t.Completed)})
		case "resume", "pause":
			if r.Method != "POST" {
				http.Error(w, "POST required", 405)
				return
			}
			var body struct{}
			if e := decodeBody(w, r, &body); e != nil {
				http.Error(w, e.Error(), 400)
				return
			}
			var e error
			if action == "resume" {
				e = m.start(id)
			} else {
				e = m.pause(id)
			}
			if e != nil {
				fail(e)
				return
			}
			jsonReply(w, map[string]bool{"ok": true})
		case "export":
			if r.Method != "GET" {
				http.Error(w, "GET required", 405)
				return
			}
			format := r.URL.Query().Get("format")
			if format != "json" && format != "csv" {
				http.Error(w, "format must be json or csv", 400)
				return
			}
			m.mu.Lock()
			t := m.tasks[id]
			var rs []Result
			if t != nil {
				rs = append([]Result{}, t.Results...)
			}
			m.mu.Unlock()
			if t == nil {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=ipdata-%s.%s", id, format))
			if format == "csv" {
				w.Header().Set("Content-Type", "text/csv; charset=utf-8")
				_, _ = io.WriteString(w, "\ufeff")
			} else {
				w.Header().Set("Content-Type", "application/json")
			}
			_ = writeResults(w, rs, format)
		default:
			http.NotFound(w, r)
		}
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != "GET" {
			if r.Header.Get("Content-Type") != "application/json" {
				http.Error(w, "application/json required", 415)
				return
			}
			if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+r.Host && origin != "https://"+r.Host {
				http.Error(w, "origin rejected", 403)
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}
func decodeBody(w http.ResponseWriter, r *http.Request, v any) error {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return e
	}
	var extra any
	if e := d.Decode(&extra); e != io.EOF {
		return fmt.Errorf("expected one JSON object")
	}
	return nil
}
func queryInt(r *http.Request, k string, fallback int) (int, error) {
	s := r.URL.Query().Get(k)
	if s == "" {
		return fallback, nil
	}
	return strconv.Atoi(s)
}
