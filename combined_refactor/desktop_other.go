//go:build !windows

package main

import (
	"os/exec"
	"runtime"
)

func setupConsole()          {}
func showStartupError(error) {}

func openBrowser(target string) error {
	program := "xdg-open"
	if runtime.GOOS == "darwin" {
		program = "open"
	}
	cmd := exec.Command(program, target)
	if e := cmd.Start(); e != nil {
		return e
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
