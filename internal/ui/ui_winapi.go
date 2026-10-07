package ui

import "syscall"

const (
	whGetMessage = 3
	whCBT        = 5
	hcbtActivate = 5
	esWantReturn = 0x1000
)

var (
	modUser32               = syscall.NewLazyDLL("user32.dll")
	procSetWindowsHookExW   = modUser32.NewProc("SetWindowsHookExW")
	procUnhookWindowsHookEx = modUser32.NewProc("UnhookWindowsHookEx")
	procCallNextHookEx      = modUser32.NewProc("CallNextHookEx")
)
