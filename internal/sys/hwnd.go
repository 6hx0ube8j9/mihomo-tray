package sys

import (
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	u32 = windows.NewLazySystemDLL("user32.dll")
	k32 = windows.NewLazySystemDLL("kernel32.dll")

	procEnumWindows        = u32.NewProc("EnumWindows")
	procGetClassName       = u32.NewProc("GetClassNameW")
	procIsWindowVisible    = u32.NewProc("IsWindowVisible")
	procGetWindowThread    = u32.NewProc("GetWindowThreadProcessId")
	procGetWindowText      = u32.NewProc("GetWindowTextW")
	procSetWindowPos       = u32.NewProc("SetWindowPos")
	procShowWindow         = u32.NewProc("ShowWindow")
	procSetForeground      = u32.NewProc("SetForegroundWindow")
	procBringToTop         = u32.NewProc("BringWindowToTop")
	procGetForeground      = u32.NewProc("GetForegroundWindow")
	procAttachThread       = u32.NewProc("AttachThreadInput")
	procGetCurrentThread   = k32.NewProc("GetCurrentThreadId")
	procGetSystemMetrics   = u32.NewProc("GetSystemMetrics")
	procSwitchToThisWindow = u32.NewProc("SwitchToThisWindow")
	procSystemParametersInfo = u32.NewProc("SystemParametersInfoW")
)

const (
	SW_RESTORE     = 9
	SWP_NOSIZE     = 0x0001
	SWP_NOMOVE     = 0x0002
	SWP_SHOWWINDOW = 0x0040
	SWP_SILKY      = SWP_NOSIZE | SWP_NOMOVE | SWP_SHOWWINDOW
)

var cachedWebUIHwnd atomic.Uintptr

func init() {
	procSetContext := u32.NewProc("SetProcessDpiAwarenessContext")
	if _, _, err := procSetContext.Call(uintptr(0xfffffffc)); err != nil && uint32(err.(syscall.Errno)) != 0 {
		procSetAware := u32.NewProc("SetProcessDPIAware")
		_, _, _ = procSetAware.Call()
	}
}

func GetCachedWebUIHwnd() uintptr { return cachedWebUIHwnd.Load() }
func SetCachedWebUIHwnd(h uintptr)  { cachedWebUIHwnd.Store(h) }

func GetIdealWindowBounds() (winW, winH, winX, winY int) {
	scrWRet, _, _ := procGetSystemMetrics.Call(0)
	scrHRet, _, _ := procGetSystemMetrics.Call(1)
	scrW, scrH := int(scrWRet), int(scrHRet)

	var workArea [4]int32
	ret, _, _ := procSystemParametersInfo.Call(0x0030, 0, uintptr(unsafe.Pointer(&workArea[0])), 0)
	
	usableW, usableH := scrW, scrH
	if ret != 0 {
		usableW = int(workArea[2] - workArea[0])
		usableH = int(workArea[3] - workArea[1])
	}

	if usableW <= 0 { usableW = 1200 }
	if usableH <= 0 { usableH = 800 }

	w, h := float64(usableW), float64(usableH)
	aspectRatio := w / h

	switch {
	case usableW >= 3840:
		winW, winH = 1920, 1080

	case aspectRatio > 2.0:
		winW, winH = 1440, 900

	case aspectRatio <= 1.05:
		winW = int(w * 0.88)
		winH = int(h * 0.55)
		if winW < 850 { winW = 850 }
		if winH < 650 { winH = 650 }

	case usableW >= 2560:
		winW, winH = 1600, 960

	case usableW >= 1920:
		winW, winH = 1280, 800
		
	case usableW >= 1440:
		winW, winH = 1150, 720	
		
	case usableW <= 1280:
		winW = int(w * 0.92)
		winH = int(h * 0.88)
		if winW < 800 { winW = 800 }
		if winH < 600 { winH = 600 }
		
	default:
		winW = int(w * 0.82)
		winH = int(h * 0.80)
		if winW < 960 { winW = 960 }
		if winH < 640 { winH = 640 }
	}

	if winW > usableW { winW = int(w * 0.95) }
	if winH > usableH { winH = int(h * 0.95) }

	if ret != 0 {
		winX = int(workArea[0]) + (usableW-winW)/2
		winY = int(workArea[1]) + (usableH-winH)/2
	} else {
		winX = (scrW - winW) / 2
		winY = (scrH - winH) / 2
	}

	if winX < 0 { winX = 0 }
	if winY < 0 { winY = 0 }

	return
}

func FindAndFocusAppWindow(exactTitle string, mainPid uint32) bool {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	var foundHwnd uintptr
	targetTitleLower := strings.ToLower(strings.TrimSpace(exactTitle))

	cb := windows.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		if !IsWindowVisible(hwnd) {
			return 1
		}

		var clsBuf [256]uint16
		procGetClassName.Call(hwnd, uintptr(unsafe.Pointer(&clsBuf[0])), 256)
		if !strings.HasPrefix(windows.UTF16ToString(clsBuf[:]), "Chrome_WidgetWin") {
			return 1
		}

		var wndPid uint32
		procGetWindowThread.Call(hwnd, uintptr(unsafe.Pointer(&wndPid)))

		var titleBuf [512]uint16
		procGetWindowText.Call(hwnd, uintptr(unsafe.Pointer(&titleBuf[0])), 512)
		wndTitle := strings.ToLower(strings.TrimSpace(windows.UTF16ToString(titleBuf[:])))
		if mainPid != 0 && wndPid == mainPid {
			if wndTitle != "" {
				foundHwnd = hwnd
				SetCachedWebUIHwnd(hwnd)
				return 0
			}
		}

		if targetTitleLower != "" {
			if wndTitle == targetTitleLower ||
				wndTitle == targetTitleLower+" - google chrome" ||
				wndTitle == targetTitleLower+" - microsoft edge" ||
				wndTitle == targetTitleLower+" - brave" ||
				wndTitle == targetTitleLower+" - vivaldi" {
				foundHwnd = hwnd
				SetCachedWebUIHwnd(hwnd)
				return 0
			}
		}
		return 1
	})

	procEnumWindows.Call(cb, 0)

	if foundHwnd != 0 {
		FocusWindowSilky(foundHwnd)
		return true
	}
	return false
}

func FocusWindowSilky(targetHwnd uintptr) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	currT, _, _ := procGetCurrentThread.Call()
	foreH, _, _ := procGetForeground.Call()
	foreT, _, _ := procGetWindowThread.Call(foreH, 0)
	targT, _, _ := procGetWindowThread.Call(targetHwnd, 0)
	if foreT != currT && foreT != 0 {
		procAttachThread.Call(foreT, currT, 1)
	}
	if targT != 0 && targT != currT {
		procAttachThread.Call(currT, targT, 1)
	}
	procShowWindow.Call(targetHwnd, SW_RESTORE)
	procSwitchToThisWindow.Call(targetHwnd, 1)
	procSetForeground.Call(targetHwnd)
	procBringToTop.Call(targetHwnd)
	procSetWindowPos.Call(targetHwnd, uintptr(0xFFFFFFFFFFFFFFFF), 0, 0, 0, 0, SWP_SILKY)
	if targT != 0 && targT != currT {
		procAttachThread.Call(currT, targT, 0)
	}
	if foreT != currT && foreT != 0 {
		procAttachThread.Call(foreT, currT, 0)
	}
	time.AfterFunc(400*time.Millisecond, func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		procSetWindowPos.Call(targetHwnd, uintptr(0xFFFFFFFFFFFFFFFE), 0, 0, 0, 0, SWP_SILKY)
	})
}

func IsWindowVisible(hwnd uintptr) bool {
	vis, _, _ := procIsWindowVisible.Call(hwnd)
	return vis != 0
}

func IsSystemShuttingDown() bool {
	const SM_SHUTTINGDOWN = 0x2000
	r, _, _ := procGetSystemMetrics.Call(SM_SHUTTINGDOWN)
	return r != 0
}
