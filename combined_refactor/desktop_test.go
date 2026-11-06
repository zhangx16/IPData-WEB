package main

import (
	"net"
	"path/filepath"
	"testing"
)

func TestPortableDataDirectory(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "中文路径 with spaces", "IPData-WEB.exe")
	if got, want := defaultDataDirectory(exe, "windows"), filepath.Join(filepath.Dir(exe), "ipdata-tasks"); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	if got := defaultDataDirectory(exe, "linux"); got != "ipdata-tasks" {
		t.Fatal(got)
	}
}

func TestBrowserURL(t *testing.T) {
	for _, tt := range []struct{ ip, want string }{{"0.0.0.0", "http://127.0.0.1:13335"}, {"127.0.0.1", "http://127.0.0.1:13335"}, {"::", "http://[::1]:13335"}, {"::1", "http://[::1]:13335"}} {
		if got := browserURL(&net.TCPAddr{IP: net.ParseIP(tt.ip), Port: 13335}); got != tt.want {
			t.Errorf("got %s want %s", got, tt.want)
		}
	}
}

func TestPortConflictFallback(t *testing.T) {
	occupied, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer occupied.Close()
	if listener, e := listenForApp(occupied.Addr().String(), false); e == nil {
		listener.Close()
		t.Fatal("explicit port conflict should fail")
	}
	fallback, e := listenForApp(occupied.Addr().String(), true)
	if e != nil {
		t.Fatal(e)
	}
	defer fallback.Close()
	if fallback.Addr().String() == occupied.Addr().String() {
		t.Fatal("fallback reused occupied port")
	}
	if fallback.Addr().(*net.TCPAddr).IP.String() != "127.0.0.1" {
		t.Fatal("fallback must stay on loopback")
	}
}
