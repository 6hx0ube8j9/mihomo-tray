package webui

import (
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
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

	// 窗口保护边界尺寸
	minWindowWidth  = 1000
	minWindowHeight = 680
	maxWindowWidth  = 2400
	maxWindowHeight = 1350
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

	if winW < minWindowWidth { winW = minWindowWidth }
	if winH < minWindowHeight { winH = minWindowHeight }
	if winW > maxWindowWidth { winW = maxWindowWidth }
	if winH > maxWindowHeight { winH = maxWindowHeight }

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

func GetProcessIdByPort(port string) uint32 {
	cmd := exec.Command("cmd", "/c", "netstat -ano")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.Output()
	if err != nil {
		return 0
	}

	targetSearch := ":" + port
	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		if strings.Contains(line, "LISTENING") && strings.Contains(line, targetSearch) {
			fields := strings.Fields(line)
			if len(fields) > 0 {
				pidStr := fields[len(fields)-1]
				pid, _ := strconv.ParseUint(pidStr, 10, 32)
				return uint32(pid)
			}
		}
	}
	return 0
}

func FindAndFocusAppWindow(cdpTitle string, appHostPort string, mainPid uint32) bool {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	var pidMatchedHwnd uintptr
	var titleMatchedHwnd uintptr
	var anchorMatchedHwnd uintptr

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

		isPidMatch := (mainPid != 0 && wndPid == mainPid)
		isTitleMatch := (exactTargetTitle != "" && wndTitle == exactTargetTitle)
		isAnchorMatch := (fallbackAnchor != "" && strings.Contains(wndTitleLower, fallbackAnchor))

		if isPidMatch && (isTitleMatch || isAnchorMatch) {
			pidMatchedHwnd = hwnd
			return 0
		}

		if isTitleMatch && titleMatchedHwnd == 0 {
			titleMatchedHwnd = hwnd
		} else if isAnchorMatch && anchorMatchedHwnd == 0 {
			anchorMatchedHwnd = hwnd
		}

		return 1
	})

	procEnumWindows.Call(cb, 0)
	var targetHwnd uintptr
	if pidMatchedHwnd != 0 {
		targetHwnd = pidMatchedHwnd
	} else if mainPid == 0 {
		if titleMatchedHwnd != 0 {
			targetHwnd = titleMatchedHwnd
		} else if anchorMatchedHwnd != 0 {
			targetHwnd = anchorMatchedHwnd
		}
	}

	if targetHwnd != 0 {
		slog.Debug("获取目标窗口句柄并尝试接管", "Hwnd", targetHwnd)
		SetCachedWebUIHwnd(targetHwnd)
		FocusWindowSilky(targetHwnd)
		return true
	}
	return false
}

func FocusWindowSilky(targetHwnd uintptr) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	foreHwnd, _, _ := procGetForeground.Call()
	var forePid uint32
	foreThread, _, _ := procGetWindowThread.Call(foreHwnd, uintptr(unsafe.Pointer(&forePid)))

	currThread, _, _ := procGetCurrentThread.Call()
	myPid := uint32(os.Getpid())

	attached := false
	if foreThread != 0 && foreThread != currThread && forePid != myPid {
		slog.Debug("附加输入线程以申请前台焦点权限")
		ret, _, _ := procAttachThread.Call(foreThread, currThread, 1)
		attached = (ret != 0)
	} else {
		slog.Debug("当前进程已具备前台权限，跳过线程附加")
	}

	procShowWindow.Call(targetHwnd, SW_RESTORE)
	procSetForeground.Call(targetHwnd)
	procBringToTop.Call(targetHwnd)

	if attached {
		procAttachThread.Call(foreThread, currThread, 0)
	}
}

func IsWindowVisible(hwnd uintptr) bool {
	vis, _, _ := procIsWindowVisible.Call(hwnd)
	return vis != 0
}
