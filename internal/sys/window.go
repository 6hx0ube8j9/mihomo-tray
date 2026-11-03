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
	procSetForeground        = modUser32.NewProc("SetForegroundWindow")
	procGetWindow            = modUser32.NewProc("GetWindow")
	procIsIconic             = modUser32.NewProc("IsIconic")
	procIsWindow             = modUser32.NewProc("IsWindow")
	procIsHungAppWindow      = modUser32.NewProc("IsHungAppWindow")
	procSetProcessDpiContext = modUser32.NewProc("SetProcessDpiAwarenessContext")
	procSetProcessDPIAware   = modUser32.NewProc("SetProcessDPIAware")
	procGetSystemMetrics     = modUser32.NewProc("GetSystemMetrics")
	procSystemParametersInfo = modUser32.NewProc("SystemParametersInfoW")
	procGetCursorPos         = modUser32.NewProc("GetCursorPos")
	procMonitorFromPoint     = modUser32.NewProc("MonitorFromPoint")
	procGetMonitorInfoW      = modUser32.NewProc("GetMonitorInfoW")
)

const (
	SW_HIDE                  = 0
	SW_RESTORE               = 9
	SW_SHOW                  = 5
	GW_OWNER                 = 4
	SPI_GETWORKAREA          = 0x0030
	SM_CXSCREEN              = 0
	SM_CYSCREEN              = 1
	MONITOR_DEFAULTTONEAREST = 2

	dpiAwarenessPerMonitorV2 = uintptr(0xFFFFFFFC)
)

// WindowInfo 顶层窗口枚举承载对象
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

type point struct {
	X int32
	Y int32
}

type rect struct {
	Left   int32
	Top    int32
	Right  int32
	Bottom int32
}

type monitorInfo struct {
	CbSize    uint32
	RcMonitor rect
	RcWork    rect
	DwFlags   uint32
}

func init() {
	if _, _, err := procSetProcessDpiContext.Call(dpiAwarenessPerMonitorV2); err != nil && uint32(err.(syscall.Errno)) != 0 {
		_, _, _ = procSetProcessDPIAware.Call()
	}
}

func GetDisplayWorkArea() (usableW, usableH, startX, startY int) {
 
	return 1920, 1080, 0, 0
}

var enumWindowsCallback = windows.NewCallback(func(hwnd uintptr, lParam unsafe.Pointer) uintptr {
	if lParam == nil || !IsWindowVisible(hwnd) {
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

	ctx := (*enumContext)(lParam)
	if ctx.matcher(WindowInfo{Hwnd: hwnd, Pid: wndPid, ClassName: clsName, Title: wndTitle}) {
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
	runtime.KeepAlive(ctx)
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

func IsIconic(hwnd uintptr) bool {
	min, _, _ := procIsIconic.Call(hwnd)
	return min != 0
}

func IsWindow(hwnd uintptr) bool {
	if hwnd == 0 {
		return false
	}
	ret, _, _ := procIsWindow.Call(hwnd)
	return ret != 0
}

func IsWindowHung(hwnd uintptr) bool {
	if hwnd == 0 {
		return false
	}
	ret, _, _ := procIsHungAppWindow.Call(hwnd)
	return ret != 0
}

func IsWindowAliveAndResponsive(hwnd uintptr) bool {
	if hwnd == 0 {
		return false
	}
	return IsWindow(hwnd) && IsWindowVisible(hwnd) && !IsWindowHung(hwnd)
}

func AllowSetForegroundWindow(pid uint32) {
}
