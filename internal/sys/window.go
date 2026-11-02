package main

import (
	"fmt"
	"strings"
	"syscall"
	"unsafe"
)

var (
	user32               = syscall.NewLazyDLL("user32.dll")
	procEnumWindows      = user32.NewProc("EnumWindows")
	procGetWindowTextW   = user32.NewProc("GetWindowTextW")
	procIsWindowVisible  = user32.NewProc("IsWindowVisible")
	procShowWindow       = user32.NewProc("ShowWindow")
	procBringWindowToTop = user32.NewProc("BringWindowToTop")
	procSetForeground    = user32.NewProc("SetForegroundWindow")
	procIsIconic         = user32.NewProc("IsIconic")
)

const (
	SW_RESTORE = 9
	SW_SHOW    = 5
)

func FindWindowByTitle(keyword string) uintptr {
	var targetHwnd uintptr

	cb := syscall.NewCallback(func(hwnd uintptr, lparam uintptr) uintptr {
		vis, _, _ := procIsWindowVisible.Call(hwnd)
		if vis == 0 {
			return 1
		}

		var buf [512]uint16
		procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), 512)
		title := syscall.UTF16ToString(buf[:])

		if strings.Contains(strings.ToLower(title), strings.ToLower(keyword)) {
			targetHwnd = hwnd
			return 0
		}
		return 1
	})

	procEnumWindows.Call(cb, 0)
	return targetHwnd
}

func FocusWindow(hwnd uintptr) {
	if hwnd == 0 {
		return
	}

	isIconic, _, _ := procIsIconic.Call(hwnd)
	if isIconic != 0 {
		procShowWindow.Call(hwnd, SW_RESTORE)
	} else {
		procShowWindow.Call(hwnd, SW_SHOW)
	}

	procBringWindowToTop.Call(hwnd)
	procSetForeground.Call(hwnd)
}

func main() {
	keyword := "Bing"

	hwnd := FindWindowByTitle(keyword)
	if hwnd == 0 {
		fmt.Printf("未找到标题包含 [%s] 的可见窗口\n", keyword)
		return
	}

	fmt.Printf("找到窗口 HWND: 0x%X，正在置前...\n", hwnd)
	FocusWindow(hwnd)
}
