package main

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestSpeedResponseValidation(t *testing.T) {
	for _, test := range []struct {
		name     string
		status   int
		ct, body string
		ok       bool
	}{{"ok", 200, "application/octet-stream", strings.Repeat("x", 2048), true}, {"short", 200, "application/octet-stream", "short", false}, {"challenge", 200, "text/html", strings.Repeat("x", 2048), false}, {"redirect", 302, "application/octet-stream", strings.Repeat("x", 2048), false}} {
		t.Run(test.name, func(t *testing.T) {
			s := pipeServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.TLS.ServerName != "download.example" || r.Host != "download.example" {
					t.Error("SNI/Host mismatch")
				}
				w.Header().Set("Content-Type", test.ct)
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer s.Close()
			c := defaults()
			c.SpeedURL = "https://download.example/data"
			c.SpeedBytes = 1024
			c.Insecure = true
			r := speedWithDial(context.Background(), Target{"127.0.0.1", 443}, c, s.pipe.dial)
			if test.ok != (r.Error == "") {
				t.Fatalf("%+v", r)
			}
		})
	}
}
