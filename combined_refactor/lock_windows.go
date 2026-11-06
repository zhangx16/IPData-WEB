package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

func lockData(dir string) (func(), error) {
	f, e := os.OpenFile(filepath.Join(dir, ".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	kernel := syscall.NewLazyDLL("kernel32.dll")
	lock := kernel.NewProc("LockFileEx")
	unlock := kernel.NewProc("UnlockFileEx")
	o := new(syscall.Overlapped)
	ok, _, err := lock.Call(f.Fd(), 3, 0, 1, 0, uintptr(unsafe.Pointer(o)))
	if ok == 0 {
		f.Close()
		return nil, fmt.Errorf("任务目录已被其他进程使用: %v", err)
	}
	return func() { unlock.Call(f.Fd(), 0, 1, 0, uintptr(unsafe.Pointer(o))); f.Close() }, nil
}
