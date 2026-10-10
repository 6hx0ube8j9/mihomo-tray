package sys

import (
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procGetCurrentThread      = modKernel32.NewProc("GetCurrentThreadId")
	procEnumWindows           = modUser32.NewProc("EnumWindows")
	procGetClassName          = modUser32.NewProc("GetClassNameW")
	procIsWindowVisible       = modUser32.NewProc("IsWindowVisible")
	procGetWindowThread       = modUser32.NewProc("GetWindowThreadProcessId")
	procGetWindowText         = modUser32.NewProc("GetWindowTextW")
	procShowWindow            = modUser32.NewProc("ShowWindow")
	procBringToTop            = modUser32.NewProc("BringWindowToTop")
	procGetForeground         = modUser32.NewProc("GetForegroundWindow")
	procAttachThread          = modUser32.NewProc("AttachThreadInput")
	procSystemParametersInfo  = modUser32.NewProc("SystemParametersInfoW")
	procSetProcessDpiContext  = modUser32.NewProc("SetProcessDpiAwarenessContext")
	procSetProcessDPIAware    = modUser32.NewProc("SetProcessDPIAware")
	procGetSystemMetrics      = modUser32.NewProc("GetSystemMetrics")
	procSetForeground         = modUser32.NewProc("SetForegroundWindow")
	procGetWindow             = modUser32.NewProc("GetWindow")
	procAllowSetForegroundWnd = modUser32.NewProc("AllowSetForegroundWindow")
)

const (
	SW_RESTORE      = 9
	GW_OWNER        = 4
	SPI_GETWORKAREA = 0x0030
	SM_CXSCREEN     = 0
	SM_CYSCREEN     = 1
	ASFW_ANY        = 0xFFFFFFFF

	dpiAwarenessPerMonitorV2 = uintptr(0xFFFFFFFC) // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 (-4)
)

type WindowInfo struct {
	Hwnd      uintptr
	Pid       uint32
	ClassName string
	Title     string
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

func FindTopWindow(matcher func(info WindowInfo) bool) uintptr {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	var targetHwnd uintptr

	cb := windows.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
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

		if matcher(info) {
			targetHwnd = hwnd
			return 0
		}
		return 1
	})

	procEnumWindows.Call(cb, 0)
	return targetHwnd
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

// GrantForegroundPrivilege grants foreground activation rights to the background instance.
// Called by the secondary instance in main.go before sending the wake-up event.
// DO NOT DELETE: Removing this will trigger Windows focus stealing prevention, 
// causing the WebUI window to lag for 0.5s and appear behind other windows.
func GrantForegroundPrivilege() {
	procAllowSetForegroundWnd.Call(ASFW_ANY)
}
