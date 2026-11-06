package main

import (
	"fmt"
	"syscall"
	"unsafe"
)

func setupConsole() {
	kernel := syscall.NewLazyDLL("kernel32.dll")
	kernel.NewProc("SetConsoleOutputCP").Call(65001)
	kernel.NewProc("SetConsoleCP").Call(65001)
	title, _ := syscall.UTF16PtrFromString("IPData-WEB " + buildVersion + " — Ctrl+C 暂停并退出")
	kernel.NewProc("SetConsoleTitleW").Call(uintptr(unsafe.Pointer(title)))
}

func openBrowser(target string) error {
	operation, _ := syscall.UTF16PtrFromString("open")
	url, e := syscall.UTF16PtrFromString(target)
	if e != nil {
		return e
	}
	result, _, _ := syscall.NewLazyDLL("shell32.dll").NewProc("ShellExecuteW").Call(0, uintptr(unsafe.Pointer(operation)), uintptr(unsafe.Pointer(url)), 0, 0, 1)
	if result <= 32 {
		return fmt.Errorf("无法打开默认浏览器（ShellExecuteW=%d）", result)
	}
	return nil
}

func showStartupError(err error) {
	title, _ := syscall.UTF16PtrFromString("IPData-WEB 启动失败")
	text, _ := syscall.UTF16PtrFromString(err.Error() + "\n\n如果已启动一个实例，请回到原窗口继续使用。")
	syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), 0x10)
}
