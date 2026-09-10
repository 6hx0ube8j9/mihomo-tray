package sys

import (
	"log/slog"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modUser32Window   = windows.NewLazySystemDLL("user32.dll")
	modKernel32Window = windows.NewLazySystemDLL("kernel32.dll")

	procGetCurrentThread     = modKernel32Window.NewProc("GetCurrentThreadId")
	procEnumWindows          = modUser32Window.NewProc("EnumWindows")
	procGetClassName         = modUser32Window.NewProc("GetClassNameW")
	procIsWindowVisible      = modUser32Window.NewProc("IsWindowVisible")
	procGetWindowThread      = modUser32Window.NewProc("GetWindowThreadProcessId")
	procGetWindowText        = modUser32Window.NewProc("GetWindowTextW")
	procSetWindowPos         = modUser32Window.NewProc("SetWindowPos")
	procShowWindow           = modUser32Window.NewProc("ShowWindow")
	procBringToTop           = modUser32Window.NewProc("BringWindowToTop")
	procGetForeground        = modUser32Window.NewProc("GetForegroundWindow")
	procAttachThread         = modUser32Window.NewProc("AttachThreadInput")
	procSwitchToThisWindow   = modUser32Window.NewProc("SwitchToThisWindow")
	procSystemParametersInfo = modUser32Window.NewProc("SystemParametersInfoW")
	procSetProcessDpiContext = modUser32Window.NewProc("SetProcessDpiAwarenessContext")
	procSetProcessDPIAware   = modUser32Window.NewProc("SetProcessDPIAware")
	procGetSystemMetrics     = modUser32Window.NewProc("GetSystemMetrics")
	procSetForeground        = modUser32Window.NewProc("SetForegroundWindow")
	procGetWindow            = modUser32Window.NewProc("GetWindow")
)

const (
	SW_RESTORE     = 9
	SWP_NOSIZE     = 0x0001
	SWP_NOMOVE     = 0x0002
	SWP_NOACTIVATE = 0x0010
	SWP_SHOWWINDOW = 0x0040
	SWP_SILKY      = SWP_NOSIZE | SWP_NOMOVE | SWP_SHOWWINDOW
	SWP_SILKY_OFF  = SWP_NOSIZE | SWP_NOMOVE | SWP_NOACTIVATE
)

var (
	hwndTopmost   = ^uintptr(0)
	hwndNoTopmost = ^uintptr(1)

	cachedWebUIHwnd atomic.Uintptr
)

func init() {
	if _, _, err := procSetProcessDpiContext.Call(uintptr(0xfffffffc)); err != nil && uint32(err.(syscall.Errno)) != 0 {
		_, _, _ = procSetProcessDPIAware.Call()
	}
}

func GetCachedWebUIHwnd() uintptr { return cachedWebUIHwnd.Load() }
func SetCachedWebUIHwnd(h uintptr) { cachedWebUIHwnd.Store(h) }

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

	if usableW <= 0 { usableW = 1280 }
	if usableH <= 0 { usableH = 800 }

	w, h := float64(usableW), float64(usableH)
	aspectRatio := w / h

	switch {
	case aspectRatio >= 2.0:
		winW = int(w * 0.55)
		winH = int(h * 0.82)
	case aspectRatio <= 1.15:
		winW = int(w * 0.92)
		winH = int(h * 0.60)
	default:
		winW = int(w * 0.72)
		winH = int(h * 0.80)
	}

	if winW < 1000 { winW = 1000 }
	if winH < 680  { winH = 680 }
	if winW > 2400 { winW = 2400 }
	if winH > 1350 { winH = 1350 }

	if winW > usableW { winW = int(w * 0.96) }
	if winH > usableH { winH = int(h * 0.96) }

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


func isStandardBrowserWindow(titleLower string) bool {
	suffixes := []string{
		" - google chrome",
		" - microsoft edge",
		" - brave",
		" - vivaldi",
		" - firefox",
	}

	for _, suffix := range suffixes {
		if strings.HasSuffix(titleLower, suffix) {
			return true
		}
	}
	return false
}

func FindAndFocusAppWindow(cdpTitle string, appHostPort string, mainPid uint32) bool {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	var foundHwnd uintptr
	exactTargetTitle := strings.TrimSpace(cdpTitle)
	fallbackAnchor := strings.ToLower(strings.TrimSpace(appHostPort))

	cb := windows.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		if !IsWindowVisible(hwnd) {
			return 1
		}
		
		owner, _, _ := procGetWindow.Call(hwnd, 4)
		if owner != 0 {
			return 1
		}

		var clsBuf [256]uint16
		procGetClassName.Call(hwnd, uintptr(unsafe.Pointer(&clsBuf[0])), 256)
		clsName := windows.UTF16ToString(clsBuf[:])
		if !strings.HasPrefix(clsName, "Chrome_WidgetWin") && !strings.HasPrefix(clsName, "MozillaWindowClass") {
			return 1
		}

		var titleBuf [512]uint16
		procGetWindowText.Call(hwnd, uintptr(unsafe.Pointer(&titleBuf[0])), 512)
		wndTitle := strings.TrimSpace(windows.UTF16ToString(titleBuf[:]))
		if wndTitle == "" {
			return 1
		}

		wndTitleLower := strings.ToLower(wndTitle)

		if isStandardBrowserWindow(wndTitleLower) {
			return 1
		}

		var wndPid uint32
		procGetWindowThread.Call(hwnd, uintptr(unsafe.Pointer(&wndPid)))

		if mainPid != 0 && wndPid == mainPid {
			foundHwnd = hwnd
			return 0
		}

		if exactTargetTitle != "" && wndTitle == exactTargetTitle {
			foundHwnd = hwnd
			return 0
		}

		if fallbackAnchor != "" && strings.Contains(wndTitleLower, fallbackAnchor) {
			foundHwnd = hwnd
			return 0
		}

		return 1
	})

	procEnumWindows.Call(cb, 0)

    if foundHwnd != 0 {
		slog.Debug("通过句柄接管目标浏览器进程", "Hwnd", foundHwnd)
		SetCachedWebUIHwnd(foundHwnd) 
		FocusWindowSilky(foundHwnd)
		return true
	}
	return false
}

func FocusWindowSilky(targetHwnd uintptr) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	slog.Debug("附加线程输入以强制置顶窗口 (AttachThreadInput)")
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
	procSetWindowPos.Call(targetHwnd, hwndTopmost, 0, 0, 0, 0, SWP_SILKY)

	if targT != 0 && targT != currT {
		procAttachThread.Call(currT, targT, 0)
	}
	if foreT != currT && foreT != 0 {
		procAttachThread.Call(foreT, currT, 0)
	}

	time.AfterFunc(400*time.Millisecond, func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		procSetWindowPos.Call(targetHwnd, hwndNoTopmost, 0, 0, 0, 0, SWP_SILKY_OFF)
	})
}

func IsWindowVisible(hwnd uintptr) bool {
	vis, _, _ := procIsWindowVisible.Call(hwnd)
	return vis != 0
}
