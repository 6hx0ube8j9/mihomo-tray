//go:build windows

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
	modShcore            = windows.NewLazySystemDLL("shcore.dll")
	procGetDpiForMonitor = modShcore.NewProc("GetDpiForMonitor")

	procEnumWindows          = modUser32.NewProc("EnumWindows")
	procGetClassName         = modUser32.NewProc("GetClassNameW")
	procIsWindowVisible      = modUser32.NewProc("IsWindowVisible")
	procGetWindowThread      = modUser32.NewProc("GetWindowThreadProcessId")
	procGetWindowText        = modUser32.NewProc("GetWindowTextW")
	procShowWindow           = modUser32.NewProc("ShowWindow")
	procSetForeground        = modUser32.NewProc("SetForegroundWindow")
	procGetForegroundWindow  = modUser32.NewProc("GetForegroundWindow")
	procSetFocus             = modUser32.NewProc("SetFocus")
	procBringWindowToTop     = modUser32.NewProc("BringWindowToTop")
	procSetWindowPos         = modUser32.NewProc("SetWindowPos")
	procGetWindowRect        = modUser32.NewProc("GetWindowRect")
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
	procGetCursorPos         = modUser32.NewProc("GetCursorPos")
	procMonitorFromRect      = modUser32.NewProc("MonitorFromRect")
	procMonitorFromWindow    = modUser32.NewProc("MonitorFromWindow")
	procGetMonitorInfoW      = modUser32.NewProc("GetMonitorInfoW")
)

const (
	swHide    = 0
	swShow    = 5
	swRestore = 9

	gwOwner         = 4
	spiGetWorkArea  = 0x0030
	smCxScreen      = 0
	smCyScreen      = 1
	asfwAny         = 0xFFFFFFFF
	mdtEffectiveDPI = 0

	monitorDefaultToPrimary = 1
	monitorDefaultToNearest = 2
	monitorInfoPrimary      = 1

	hwndTop       = 0
	hwndTopMost   = ^uintptr(0) // -1
	hwndNoTopMost = ^uintptr(1) // -2

	swpNoSize     = 0x0001
	swpNoMove     = 0x0002
	swpNoZOrder   = 0x0004
	swpNoActivate = 0x0010

	dpiAwarenessPerMonitorV2 = ^uintptr(3) // -4
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

// DisplayMetrics describes the work area, monitor origin, and DPI scale of a display.
type DisplayMetrics struct {
	WorkLeft        int
	WorkTop         int
	WorkWidth       int
	WorkHeight      int
	MonitorLeft     int
	MonitorTop      int
	DPIScale        float64
	PrimaryDPIScale float64
}

// WindowInfo summarizes a top-level Win32 window returned during enumeration.
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

// -------------------- Display & Monitor --------------------

// GetCursorDisplayMetrics returns metrics for the display containing the mouse cursor.
func GetCursorDisplayMetrics() DisplayMetrics {
	var pt point
	if ret, _, _ := procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt))); ret != 0 {
		r := rect{
			Left:   pt.X,
			Top:    pt.Y,
			Right:  pt.X + 1,
			Bottom: pt.Y + 1,
		}
		hMonitor, _, _ := procMonitorFromRect.Call(uintptr(unsafe.Pointer(&r)), monitorDefaultToNearest)
		if mi, ok := queryMonitorInfo(hMonitor); ok {
			workW := int(mi.RcWork.Right - mi.RcWork.Left)
			workH := int(mi.RcWork.Bottom - mi.RcWork.Top)
			if workW > 0 && workH > 0 {
				scale := monitorDPIScale(hMonitor)
				primaryScale := scale
				if mi.DwFlags&monitorInfoPrimary == 0 {
					primaryScale = primaryMonitorDPIScale()
				}
				return DisplayMetrics{
					WorkLeft:        int(mi.RcWork.Left),
					WorkTop:         int(mi.RcWork.Top),
					WorkWidth:       workW,
					WorkHeight:      workH,
					MonitorLeft:     int(mi.RcMonitor.Left),
					MonitorTop:      int(mi.RcMonitor.Top),
					DPIScale:        scale,
					PrimaryDPIScale: primaryScale,
				}
			}
		}
	}

	scale := primaryMonitorDPIScale()

	var workArea [4]int32
	if ret, _, _ := procSystemParametersInfo.Call(spiGetWorkArea, 0, uintptr(unsafe.Pointer(&workArea[0])), 0); ret != 0 {
		workW := int(workArea[2] - workArea[0])
		workH := int(workArea[3] - workArea[1])
		if workW > 0 && workH > 0 {
			return DisplayMetrics{
				WorkLeft:        int(workArea[0]),
				WorkTop:         int(workArea[1]),
				WorkWidth:       workW,
				WorkHeight:      workH,
				DPIScale:        scale,
				PrimaryDPIScale: scale,
			}
		}
	}

	scrW, _, _ := procGetSystemMetrics.Call(smCxScreen)
	scrH, _, _ := procGetSystemMetrics.Call(smCyScreen)
	return DisplayMetrics{
		WorkWidth:       int(scrW),
		WorkHeight:      int(scrH),
		DPIScale:        scale,
		PrimaryDPIScale: scale,
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

func primaryMonitorDPIScale() float64 {
	r := rect{Left: 0, Top: 0, Right: 1, Bottom: 1}
	hPrimary, _, _ := procMonitorFromRect.Call(uintptr(unsafe.Pointer(&r)), monitorDefaultToPrimary)
	return monitorDPIScale(hPrimary)
}

func queryMonitorInfo(hMonitor uintptr) (monitorInfo, bool) {
	if hMonitor == 0 {
		return monitorInfo{}, false
	}
	var mi monitorInfo
	mi.CbSize = uint32(unsafe.Sizeof(mi))
	ret, _, _ := procGetMonitorInfoW.Call(hMonitor, uintptr(unsafe.Pointer(&mi)))
	return mi, ret != 0
}

func queryWindowRect(hwnd uintptr) (rect, bool) {
	var r rect
	ret, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
	return r, ret != 0
}

func cursorWorkArea() (workW, workH, workLeft, workTop int) {
	m := GetCursorDisplayMetrics()
	return m.WorkWidth, m.WorkHeight, m.WorkLeft, m.WorkTop
}

func windowWorkArea(hwnd uintptr) (workW, workH, workLeft, workTop int) {
	if hwnd != 0 {
		hMonitor, _, _ := procMonitorFromWindow.Call(hwnd, monitorDefaultToNearest)
		if mi, ok := queryMonitorInfo(hMonitor); ok {
			w := int(mi.RcWork.Right - mi.RcWork.Left)
			h := int(mi.RcWork.Bottom - mi.RcWork.Top)
			if w > 0 && h > 0 {
				return w, h, int(mi.RcWork.Left), int(mi.RcWork.Top)
			}
		}
	}
	return cursorWorkArea()
}

func clampCoord(pos, minPos, maxPos int) int {
	if maxPos < minPos {
		return minPos
	}
	if pos < minPos {
		return minPos
	}
	if pos > maxPos {
		return maxPos
	}
	return pos
}

// -------------------- Positioning & Layout --------------------

// CenterWindow centers hwnd within the cursor display's work area, clamping its size if oversized.
func CenterWindow(hwnd uintptr) {
	if hwnd == 0 {
		return
	}

	r, ok := queryWindowRect(hwnd)
	if !ok {
		return
	}

	winW := int(r.Right - r.Left)
	winH := int(r.Bottom - r.Top)
	workW, workH, workLeft, workTop := cursorWorkArea()

	flags := uintptr(swpNoZOrder | swpNoActivate | swpNoSize)
	if workW > 0 && winW > workW {
		winW = workW
		flags &^= swpNoSize
	}
	if workH > 0 && winH > workH {
		winH = workH
		flags &^= swpNoSize
	}

	x := clampCoord(workLeft+(workW-winW)/2, workLeft, workLeft+workW-winW)
	y := clampCoord(workTop+(workH-winH)/2, workTop, workTop+workH-winH)

	_, _, _ = procSetWindowPos.Call(hwnd, 0, uintptr(int32(x)), uintptr(int32(y)), 0, 0, swpNoSize|swpNoZOrder|swpNoActivate)
}

// CenterWindowOnOwner centers hwnd over ownerHwnd within the owner's monitor work area,
// or falls back to the cursor display's work area when ownerHwnd is unavailable.
func CenterWindowOnOwner(hwnd, ownerHwnd uintptr) {
	if hwnd == 0 {
		return
	}

	popRect, ok := queryWindowRect(hwnd)
	if !ok {
		return
	}
	winW := int(popRect.Right - popRect.Left)
	winH := int(popRect.Bottom - popRect.Top)

	if ownerHwnd != 0 && IsWindowVisible(ownerHwnd) && !IsWindowMinimized(ownerHwnd) {
		if ownerRect, ok := queryWindowRect(ownerHwnd); ok {
			workW, workH, workLeft, workTop := windowWorkArea(ownerHwnd)
			centerX := int(ownerRect.Left + (ownerRect.Right-ownerRect.Left)/2)
			centerY := int(ownerRect.Top + (ownerRect.Bottom-ownerRect.Top)/2)
			x := clampCoord(centerX-winW/2, workLeft, workLeft+workW-winW)
			y := clampCoord(centerY-winH/2, workTop, workTop+workH-winH)
			_, _, _ = procSetWindowPos.Call(hwnd, hwndTop, uintptr(int32(x)), uintptr(int32(y)), 0, 0, swpNoSize)
			return
		}
	}

	workW, workH, workLeft, workTop := cursorWorkArea()
	x := clampCoord(workLeft+(workW-winW)/2, workLeft, workLeft+workW-winW)
	y := clampCoord(workTop+(workH-winH)/2, workTop, workTop+workH-winH)
	_, _, _ = procSetWindowPos.Call(hwnd, hwndTop, uintptr(int32(x)), uintptr(int32(y)), 0, 0, swpNoSize)
}

// -------------------- Focus & Visibility --------------------

// ActivateWindow restores a minimized window and gives it foreground focus.
func ActivateWindow(hwnd uintptr) {
	if hwnd == 0 || !IsWindowVisible(hwnd) {
		return
	}
	if IsWindowMinimized(hwnd) {
		_, _, _ = procShowWindow.Call(hwnd, swRestore)
	}
	_, _, _ = procSetForeground.Call(hwnd)
	_, _, _ = procSetFocus.Call(hwnd)
}

// BringWindowToFront forcefully raises and focuses hwnd, falling back to AttachThreadInput
// when Windows foreground lock blocks SetForegroundWindow.
func BringWindowToFront(hwnd uintptr) {
	if hwnd == 0 {
		return
	}

	if IsWindowVisible(hwnd) && !IsWindowMinimized(hwnd) {
		if fgHwnd, _, _ := procGetForegroundWindow.Call(); fgHwnd == hwnd {
			return
		}
	}

	if IsWindowMinimized(hwnd) {
		_, _, _ = procShowWindow.Call(hwnd, swRestore)
	} else if !IsWindowVisible(hwnd) {
		_, _, _ = procShowWindow.Call(hwnd, swShow)
	}

	_, _, _ = procSetWindowPos.Call(hwnd, hwndTopMost, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoActivate)
	_, _, _ = procSetWindowPos.Call(hwnd, hwndNoTopMost, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoActivate)

	_, _, _ = procBringWindowToTop.Call(hwnd)
	if ret, _, _ := procSetForeground.Call(hwnd); ret != 0 {
		return
	}

	fgHwnd, _, _ := procGetForegroundWindow.Call()
	if fgHwnd == 0 || fgHwnd == hwnd {
		return
	}

	fgThreadID, _, _ := procGetWindowThread.Call(fgHwnd, 0)
	curThreadID := uintptr(windows.GetCurrentThreadId())
	if fgThreadID == 0 || fgThreadID == curThreadID {
		return
	}

	if attached, _, _ := procAttachThreadInput.Call(curThreadID, fgThreadID, 1); attached != 0 {
		_, _, _ = procBringWindowToTop.Call(hwnd)
		_, _, _ = procSetForeground.Call(hwnd)
		_, _, _ = procAttachThreadInput.Call(curThreadID, fgThreadID, 0)
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
		_, _, _ = procShowWindow.Call(hwnd, swHide)
	}
}

// -------------------- Enumeration & Queries --------------------

var enumWindowsCallback = windows.NewCallback(func(hwnd uintptr, lParam uintptr) uintptr {
	v, ok := enumCtxMap.Load(lParam)
	if !ok || !IsWindowVisible(hwnd) {
		return 1
	}
	ctx := v.(*enumContext)

	owner, _, _ := procGetWindow.Call(hwnd, gwOwner)
	if owner != 0 {
		return 1
	}

	var clsBuf [256]uint16
	_, _, _ = procGetClassName.Call(hwnd, uintptr(unsafe.Pointer(&clsBuf[0])), 256)
	clsName := windows.UTF16ToString(clsBuf[:])

	var titleBuf [512]uint16
	_, _, _ = procGetWindowText.Call(hwnd, uintptr(unsafe.Pointer(&titleBuf[0])), 512)
	wndTitle := strings.TrimSpace(windows.UTF16ToString(titleBuf[:]))

	var wndPid uint32
	_, _, _ = procGetWindowThread.Call(hwnd, uintptr(unsafe.Pointer(&wndPid)))

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

	_, _, _ = procEnumWindows.Call(enumWindowsCallback, id)
	return ctx.targetHwnd
}

func IsWindow(hwnd uintptr) bool {
	if hwnd == 0 {
		return false
	}
	ret, _, _ := procIsWindow.Call(hwnd)
	return ret != 0
}

func IsWindowVisible(hwnd uintptr) bool {
	if hwnd == 0 {
		return false
	}
	vis, _, _ := procIsWindowVisible.Call(hwnd)
	return vis != 0
}

func IsWindowMinimized(hwnd uintptr) bool {
	if hwnd == 0 {
		return false
	}
	min, _, _ := procIsIconic.Call(hwnd)
	return min != 0
}

func IsWindowHung(hwnd uintptr) bool {
	if hwnd == 0 {
		return false
	}
	ret, _, _ := procIsHungAppWindow.Call(hwnd)
	return ret != 0
}