package sys

import (
	"strings"
	"sync"
	"sync/atomic"
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
	procSetForeground        = modUser32.NewProc("SetForegroundWindow")
	procAllowSetForeground   = modUser32.NewProc("AllowSetForegroundWindow")
	procGetWindow            = modUser32.NewProc("GetWindow")
	procIsIconic             = modUser32.NewProc("IsIconic")
	procIsWindow             = modUser32.NewProc("IsWindow")
	procIsHungAppWindow      = modUser32.NewProc("IsHungAppWindow")
	procSetProcessDpiContext = modUser32.NewProc("SetProcessDpiAwarenessContext")
	procSetProcessDPIAware   = modUser32.NewProc("SetProcessDPIAware")
	procGetSystemMetrics     = modUser32.NewProc("GetSystemMetrics")
	procSystemParametersInfo = modUser32.NewProc("SystemParametersInfoW")

	procGetCursorPos    = modUser32.NewProc("GetCursorPos")
	procMonitorFromRect = modUser32.NewProc("MonitorFromRect")
	procGetMonitorInfoW = modUser32.NewProc("GetMonitorInfoW")
)

const (
	SW_HIDE                  = 0
	SW_SHOW                  = 5
	SW_RESTORE               = 9
	GW_OWNER                 = 4
	SPI_GETWORKAREA          = 0x0030
	SM_CXSCREEN              = 0
	SM_CYSCREEN              = 1
	MONITOR_DEFAULTTONEAREST = 2

	dpiAwarenessPerMonitorV2 = uintptr(0xFFFFFFFC)
)

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

var (
	enumCtxMap sync.Map
	enumCtxID  atomic.Uintptr
)

func init() {
	if ret, _, _ := procSetProcessDpiContext.Call(dpiAwarenessPerMonitorV2); ret == 0 {
		_, _, _ = procSetProcessDPIAware.Call()
	}
}

func GetDisplayWorkArea() (usableW, usableH, startX, startY int) {
	var pt point
	if ret, _, _ := procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt))); ret != 0 {
		r := rect{
			Left:   pt.X,
			Top:    pt.Y,
			Right:  pt.X + 1,
			Bottom: pt.Y + 1,
		}
		hMonitor, _, _ := procMonitorFromRect.Call(uintptr(unsafe.Pointer(&r)), MONITOR_DEFAULTTONEAREST)
		if hMonitor != 0 {
			var mi monitorInfo
			mi.CbSize = uint32(unsafe.Sizeof(mi))
			if ret, _, _ := procGetMonitorInfoW.Call(hMonitor, uintptr(unsafe.Pointer(&mi))); ret != 0 {
				usableW = int(mi.RcWork.Right - mi.RcWork.Left)
				usableH = int(mi.RcWork.Bottom - mi.RcWork.Top)
				startX = int(mi.RcWork.Left)
				startY = int(mi.RcWork.Top)
				if usableW > 0 && usableH > 0 {
					return
				}
			}
		}
	}

	var workArea [4]int32
	if ret, _, _ := procSystemParametersInfo.Call(SPI_GETWORKAREA, 0, uintptr(unsafe.Pointer(&workArea[0])), 0); ret != 0 {
		usableW = int(workArea[2] - workArea[0])
		usableH = int(workArea[3] - workArea[1])
		startX = int(workArea[0])
		startY = int(workArea[1])
		if usableW > 0 && usableH > 0 {
			return
		}
	}

	scrW, _, _ := procGetSystemMetrics.Call(SM_CXSCREEN)
	scrH, _, _ := procGetSystemMetrics.Call(SM_CYSCREEN)
	return int(scrW), int(scrH), 0, 0
}

var enumWindowsCallback = windows.NewCallback(func(hwnd uintptr, lParam uintptr) uintptr {
	v, ok := enumCtxMap.Load(lParam)
	if !ok || !IsWindowVisible(hwnd) {
		return 1
	}
	ctx := v.(*enumContext)

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

	if ctx.matcher(WindowInfo{Hwnd: hwnd, Pid: wndPid, ClassName: clsName, Title: wndTitle}) {
		ctx.targetHwnd = hwnd
		return 0
	}
	return 1
})

func FindTopWindow(matcher func(info WindowInfo) bool) uintptr {
	ctx := &enumContext{matcher: matcher}

	id := enumCtxID.Add(1)
	enumCtxMap.Store(id, ctx)
	defer enumCtxMap.Delete(id)

	procEnumWindows.Call(enumWindowsCallback, id)
	return ctx.targetHwnd
}

func FocusWindowSilky(targetHwnd uintptr) {
	if targetHwnd == 0 {
		return
	}

	if IsIconic(targetHwnd) {
		procShowWindow.Call(targetHwnd, SW_RESTORE)
	} else if !IsWindowVisible(targetHwnd) {
		procShowWindow.Call(targetHwnd, SW_SHOW)
	}

	procSetForeground.Call(targetHwnd)
}

func AllowSetForegroundWindow(pid uint32) {
	if pid == 0 {
		pid = 0xFFFFFFFF // ASFW_ANY
	}
	_, _, _ = procAllowSetForeground.Call(uintptr(pid))
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
	return IsWindowVisible(hwnd) && !IsWindowHung(hwnd)
}
