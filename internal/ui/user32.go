// Package ui provides User32 API bindings and Win32 message constants
// required for thread-level message hooks, window subclassing, and styling.
package ui

import (
	"syscall"

	"github.com/tailscale/win"
)

const (
	// Windows Hook 常量与状态
	whGetMessage = 3
	whCBT        = 5
	hcbtActivate = 5
	pmRemove     = 1

	// 窗口与控件样式
	esWantReturn      = 0x1000
	wsExControlParent = 0x00010000
	bsDefPushButton   = 0x0001

	// 按钮消息
	bmSetStyle = 0x00F4
	bmClick    = 0x00F5

	// 会话与子类化消息
	wmQueryEndSession = 0x0011
	wmEndSession      = 0x0016
	gwlpWndProc       = -4

	// 系统参数与弹窗前台激活
	spiGetWorkArea  = 0x0030
	mbSetForeground = 0x00010000
	mbTopMost       = 0x00040000
)

const (
	hwndTopMost = ^win.HWND(0) // Win32 HWND_TOPMOST (-1)
)

var (
	modUser32               = syscall.NewLazyDLL("user32.dll")
	procSetWindowsHookExW   = modUser32.NewProc("SetWindowsHookExW")
	procUnhookWindowsHookEx = modUser32.NewProc("UnhookWindowsHookEx")
	procCallNextHookEx      = modUser32.NewProc("CallNextHookEx")
)