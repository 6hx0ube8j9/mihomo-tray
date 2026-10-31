//go:build windows

package sys

import (
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procEnumWindows          = modUser32.NewProc("EnumWindows")
	procGetClassName         = modUser32.NewProc("GetClassNameW")
	procIsWindowVisible      = modUser32.NewProc("IsWindowVisible")
	procGetWindowThread      = modUser32.NewProc("GetWindowThreadProcessId")
	procGetWindowText        = modUser32.NewProc("GetWindowTextW")
	procShowWindow           = modUser32.NewProc("ShowWindow")
	procBringToTop           = modUser32.NewProc("BringWindowToTop")
	procSystemParametersInfo = modUser32.NewProc("SystemParametersInfoW")
	procSetProcessDpiContext = modUser32.NewProc("SetProcessDpiAwarenessContext")
	procSetProcessDPIAware   = modUser32.NewProc("SetProcessDPIAware")
	procGetSystemMetrics     = modUser32.NewProc("GetSystemMetrics")
	procSetForeground        = modUser32.NewProc("SetForegroundWindow")
	procGetWindow            = modUser32.NewProc("GetWindow")
	procIsIconic             = modUser32.NewProc("IsIconic")
	procIsHungAppWindow      = modUser32.NewProc("IsHungAppWindow")
	procAllowSetForeground   = modUser32.NewProc("AllowSetForegroundWindow")
)

const (
	SW_HIDE         = 0
	SW_RESTORE      = 9
	SW_SHOW         = 5
	GW_OWNER        = 4
	SPI_GETWORKAREA = 0x0030
	SM_CXSCREEN     = 0
	SM_CYSCREEN     = 1

	dpiAwarenessPerMonitorV2 = uintptr(0xFFFFFFFC)
)

type WindowInfo struct {
	Hwnd      uintptr
	Pid       uint32
	ClassName string
	Title     string
}

type enumContext struct {
	matcher    func(info WindowInfo) bool
	targetHwnd uintptr
}

func init() {
	if _, _, err := procSetProcessDpiContext.Call(dpiAwarenessPerMonitorV2); err != nil && uint32(err.(syscall.Errno)) != 0 {
		_, _, _ = procSetProcessDPIAware.Call()
	}
}

func GetDisplayWorkArea() (usableW, usableH, startX, startY int) {
	scrWRet, _, _ := procGetSystemMetrics.Call(SM_CXSCREEN)
	scrHRet, _, _ := procGetSystemMetrics.Call(SM_CYSCREEN)
	usableW, usableH = int(scrWRet), int(scrHRet)

	var workArea [4]int32
	ret, _, _ := procSystemParametersInfo.Call(SPI_GETWORKAREA, 0, uintptr(unsafe.Pointer(&workArea[0])), 0)
	if ret != 0 {
		usableW = int(workArea[2] - workArea[0])
		usableH = int(workArea[3] - workArea[1])
		startX = int(workArea[0])
		startY = int(workArea[1])
	}
	return
}

var enumWindowsCallback = windows.NewCallback(func(hwnd uintptr, lParam uintptr) uintptr {
	ctx := (*enumContext)(unsafe.Pointer(lParam))

	if !IsWindowVisible(hwnd) {
		return 1
	}

	owner, _, _ := procGetWindow.Call(hwnd, GW_OWNER)
	if owner != 0 {
		return 1
	}

	var clsBuf [256]uint16
	procGetClassName.Call(hwnd, uintptr(unsafe.Pointer(&clsBuf[0])), 256)
	clsName := windows.UTF16ToString(clsBuf[:])

	var titleBuf [512]uint16
	procGetWindowText.Call(hwnd, uintptr(unsafe.Pointer(&titleBuf[0])), 512)
	wndTitle := strings.TrimSpace(windows.UTF16ToString(titleBuf[:]))

	var wndPid uint32
	procGetWindowThread.Call(hwnd, uintptr(unsafe.Pointer(&wndPid)))

	info := WindowInfo{
		Hwnd:      hwnd,
		Pid:       wndPid,
		ClassName: clsName,
		Title:     wndTitle,
	}

	if ctx.matcher(info) {
		ctx.targetHwnd = hwnd
		return 0
	}
	return 1
})

func FindTopWindow(matcher func(info WindowInfo) bool) uintptr {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	ctx := &enumContext{matcher: matcher}
	procEnumWindows.Call(enumWindowsCallback, uintptr(unsafe.Pointer(ctx)))
	return ctx.targetHwnd
}

func FocusWindowSilky(targetHwnd uintptr) {
	if targetHwnd == 0 {
		return
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if IsIconic(targetHwnd) {
		procShowWindow.Call(targetHwnd, SW_RESTORE)
	} else {
		procShowWindow.Call(targetHwnd, SW_SHOW)
	}

	procBringToTop.Call(targetHwnd)
	procSetForeground.Call(targetHwnd)
}

func HideWindow(hwnd uintptr) {
	if hwnd != 0 {
		procShowWindow.Call(hwnd, SW_HIDE)
	}
}

func IsWindowVisible(hwnd uintptr) bool {
	vis, _, _ := procIsWindowVisible.Call(hwnd)
	return vis != 0
}

func IsWindowHung(hwnd uintptr) bool {
	if hwnd == 0 {
		return false
	}
	ret, _, _ := procIsHungAppWindow.Call(hwnd)
	return ret != 0
}

func AllowSetForegroundWindow(pid uint32) {
	if pid != 0 {
		_, _, _ = procAllowSetForeground.Call(uintptr(pid))
	}
}

func IsIconic(hwnd uintptr) bool {
	min, _, _ := procIsIconic.Call(hwnd)
	return min != 0
}
