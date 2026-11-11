// Package ui provides User32 API bindings and Win32 message constants
// required for thread-level message hooks, window subclassing, and styling.
package ui

import "syscall"

const (
	// Windows hook constants and flags
	whGetMessage = 3
	whCBT        = 5
	hcbtActivate = 5
	pmRemove     = 1

	// Window and control styles
	esWantReturn      = 0x1000
	wsExControlParent = 0x00010000
	bsDefPushButton   = 0x0001

	// Button messages
	bmSetStyle = 0x00F4
	bmClick    = 0x00F5

	// Session and subclassing messages
	wmQueryEndSession = 0x0011
	wmEndSession      = 0x0016
	gwlpWndProc       = -4

	// System parameters and message box flags
	spiGetWorkArea  = 0x0030
	mbSetForeground = 0x00010000
	mbTopMost       = 0x00040000
)

var (
	modUser32               = syscall.NewLazyDLL("user32.dll")
	procSetWindowsHookExW   = modUser32.NewProc("SetWindowsHookExW")
	procUnhookWindowsHookEx = modUser32.NewProc("UnhookWindowsHookEx")
	procCallNextHookEx      = modUser32.NewProc("CallNextHookEx")
)