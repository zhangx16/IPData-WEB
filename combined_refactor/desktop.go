package main

import (
	"net"
	"path/filepath"
)

func defaultDataDirectory(executable, goos string) string {
	if goos == "windows" {
		return filepath.Join(filepath.Dir(executable), "ipdata-tasks")
	}
	return "ipdata-tasks"
}

func browserURL(addr net.Addr) string {
	host, port, err := net.SplitHostPort(addr.String())
	if err != nil {
		return "http://" + addr.String()
	}
	switch host {
	case "", "0.0.0.0":
		host = "127.0.0.1"
	case "::":
		host = "::1"
	}
	return "http://" + net.JoinHostPort(host, port)
}
