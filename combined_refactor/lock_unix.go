//go:build linux || darwin || freebsd || openbsd || netbsd || dragonfly

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func lockData(dir string) (func(), error) {
	f, e := os.OpenFile(filepath.Join(dir, ".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		f.Close()
		return nil, fmt.Errorf("任务目录已被其他进程使用: %w", e)
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, nil
}
