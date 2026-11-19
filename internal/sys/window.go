package sys

import (
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	shcoreDLL            = windows.NewLazySystemDLL("shcore.dll")
	procGetDpiForMonitor = shcoreDLL.NewProc("GetDpiForMonitor")

	procEnumWindows          = modUser32.NewProc("EnumWindows")
	procGetClassName         = modUser32.NewProc("GetClassNameW")
	procIsWindowVisible      = modUser32.NewProc("IsWindowVisible")
	procGetWindowThread      = modUser32.NewProc("GetWindowThreadProcessId")
	procGetWindowText        = modUser32.NewProc("GetWindowTextW")
	procShowWindow           = modUser32.NewProc("ShowWindow")
	procSetForeground        = modUser32.NewProc("SetForegroundWindow")
	procGetForegroundWindow  = modUser32.NewProc("GetForegroundWindow")
	procBringWindowToTop     = modUser32.NewProc("BringWindowToTop")
	procSetWindowPos         = modUser32.NewProc("SetWindowPos")
	procAttachThreadInput    = modUser32.NewProc("AttachThreadInput")
	procAllowSetForeground   = modUser32.NewProc("AllowSetForegroundWindow")
	procGetWindow            = modUser32.NewProc("GetWindow")
	procIsIconic             = modUser32.NewProc("IsIconic")
	procIsWindow             = modUser32.NewProc("IsWindow")
	procIsHungAppWindow      = modUser32.NewProc("IsHungAppWindow")
	procSetProcessDpiContext = modUser32.NewProc("SetProcessDpiAwarenessContext")
	procSetProcessDPIAware   = modUser32.NewProc("SetProcessDPIAware")
	procGetDpiForSystem      = modUser32.NewProc("GetDpiForSystem")
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

	hwndTopMost   = ^uintptr(0) // -1
	hwndNoTopMost = ^uintptr(1) // -2
	swpNoSize     = 0x0001
	swpNoMove     = 0x0002
	swpNoActivate = 0x0010

	dpiAwarenessPerMonitorV2 = ^uintptr(3) // -4
	mdtEffectiveDPI          = 0
	asfwAny                  = 0xFFFFFFFF
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

func monitorDPIScale(hMonitor uintptr) float64 {
	if hMonitor != 0 && procGetDpiForMonitor.Find() == nil {
		var dpiX, dpiY uint32
		hr, _, _ := procGetDpiForMonitor.Call(
			hMonitor,
			mdtEffectiveDPI,
			uintptr(unsafe.Pointer(&dpiX)),
			uintptr(unsafe.Pointer(&dpiY)),
		)
		if hr == 0 && dpiX >= 96 {
			return math.Round(float64(dpiX)/96.0*100) / 100
		}
	}
	if procGetDpiForSystem.Find() == nil {
		if dpi, _, _ := procGetDpiForSystem.Call(); dpi >= 96 {
			return math.Round(float64(dpi)/96.0*100) / 100
		}
	}
	return 1.0
}

func GetDisplayWorkArea() (usableW, usableH, startX, startY int) {
	usableW, usableH, startX, startY, _ = GetDisplayMetrics()
	return
}

func GetDisplayMetrics() (usableW, usableH, startX, startY int, dpiScale float64) {
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
					return usableW, usableH, startX, startY, monitorDPIScale(hMonitor)
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
			return usableW, usableH, startX, startY, monitorDPIScale(0)
		}
	}

	scrW, _, _ := procGetSystemMetrics.Call(SM_CXSCREEN)
	scrH, _, _ := procGetSystemMetrics.Call(SM_CYSCREEN)
	return int(scrW), int(scrH), 0, 0, monitorDPIScale(0)
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

	if IsWindowVisible(targetHwnd) && !IsIconic(targetHwnd) {
		if fgHwnd, _, _ := procGetForegroundWindow.Call(); fgHwnd == targetHwnd {
			return
		}
	}

	if IsIconic(targetHwnd) {
		procShowWindow.Call(targetHwnd, SW_RESTORE)
	} else if !IsWindowVisible(targetHwnd) {
		procShowWindow.Call(targetHwnd, SW_SHOW)
	}

	procSetWindowPos.Call(targetHwnd, hwndTopMost, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoActivate)
	procSetWindowPos.Call(targetHwnd, hwndNoTopMost, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoActivate)

	procBringWindowToTop.Call(targetHwnd)
	if ret, _, _ := procSetForeground.Call(targetHwnd); ret != 0 {
		return
	}

	fgHwnd, _, _ := procGetForegroundWindow.Call()
	if fgHwnd == 0 || fgHwnd == targetHwnd {
		return
	}

	fgThreadID, _, _ := procGetWindowThread.Call(fgHwnd, 0)
	curThreadID := uintptr(windows.GetCurrentThreadId())
	if fgThreadID == 0 || fgThreadID == curThreadID {
		return
	}

	attached, _, _ := procAttachThreadInput.Call(curThreadID, fgThreadID, 1)
	if attached != 0 {
		procBringWindowToTop.Call(targetHwnd)
		procSetForeground.Call(targetHwnd)
		procAttachThreadInput.Call(curThreadID, fgThreadID, 0)
	}
}

func AllowSetForegroundWindow(pid uint32) {
	if pid == 0 {
		pid = asfwAny
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