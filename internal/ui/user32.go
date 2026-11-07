// Package ui provides User32 API bindings and Win32 message constants
// required for thread-level message hooks, window subclassing, and styling.
package ui

import "syscall"

const (
	whGetMessage = 3
	whCBT        = 5
	hcbtActivate = 5
	esWantReturn = 0x1000
	pmRemove     = 1

	dwmwaCloak = 13
)

var (
	modUser32               = syscall.NewLazyDLL("user32.dll")
	procSetWindowsHookExW   = modUser32.NewProc("SetWindowsHookExW")
	procUnhookWindowsHookEx = modUser32.NewProc("UnhookWindowsHookEx")
	procCallNextHookEx      = modUser32.NewProc("CallNextHookEx")

	modDwmapi                = syscall.NewLazyDLL("dwmapi.dll")
	procDwmSetWindowAttribute = modDwmapi.NewProc("DwmSetWindowAttribute")
)

func setWindowCloaked(hwnd uintptr, cloaked bool) {
	if procDwmSetWindowAttribute.Find() == nil {
		var val int32
		if cloaked {
			val = 1
		}
		procDwmSetWindowAttribute.Call(hwnd, uintptr(dwmwaCloak), uintptr(unsafe.Pointer(&val)), 4)
	}
}
