package sys

import (
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

 

var (
	procEnumWindows     = modUser32.NewProc("EnumWindows")
	procGetClassName    = modUser32.NewProc("GetClassNameW")
	procIsWindowVisible = modUser32.NewProc("IsWindowVisible")
	procGetWindowThread = modUser32.NewProc("GetWindowThreadProcessId")
	procGetWindowText   = modUser32.NewProc("GetWindowTextW")
	procShowWindow      = modUser32.NewProc("ShowWindow")
	procBringToTop      = modUser32.NewProc("BringWindowToTop")
	procSetForeground   = modUser32.NewProc("SetForegroundWindow")
	procGetWindow       = modUser32.NewProc("GetWindow")
	procIsIconic        = modUser32.NewProc("IsIconic")
	procGetSystemMetrics = modUser32.NewProc("GetSystemMetrics")
    procAllowSetForeground   = modUser32.NewProc("AllowSetForegroundWindow")
	procIsHungAppWindow      = modUser32.NewProc("IsHungAppWindow")
	procSystemParametersInfo = modUser32.NewProc("SystemParametersInfoW")
	procIsWindow             = modUser32.NewProc("IsWindow")
)

const (
	SW_RESTORE = 9
	SW_SHOW    = 5
	GW_OWNER   = 4
    SPI_GETWORKAREA = 0x0030
	SM_CXSCREEN     = 0
	SM_CYSCREEN     = 1
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

var enumWindowsCallback = windows.NewCallback(func(hwnd uintptr, lParam unsafe.Pointer) uintptr {
	if lParam == nil {
		return 1
	}

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

	ctx := (*enumContext)(lParam)
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

	procBringToTop.Call(targetHwnd)
	procSetForeground.Call(targetHwnd)
}

func IsWindowVisible(hwnd uintptr) bool {
	vis, _, _ := procIsWindowVisible.Call(hwnd)
	return vis != 0
}

func IsIconic(hwnd uintptr) bool {
	min, _, _ := procIsIconic.Call(hwnd)
	return min != 0
}


func HideWindow(hwnd uintptr) {
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
 
func GetDisplayWorkArea() (usableW, usableH, startX, startY int) {
	return 1920, 1080, 0, 0
}
