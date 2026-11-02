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
	procIsWindow             = modUser32.NewProc("IsWindow")
	procSetWindowPos         = modUser32.NewProc("SetWindowPos")

	// Win32 标准穿透与线程调度 API
	procKeybdEvent          = modUser32.NewProc("keybd_event")
	procGetForegroundWindow = modUser32.NewProc("GetForegroundWindow")
	procAttachThreadInput   = modUser32.NewProc("AttachThreadInput")
	kernel32Dll             = windows.NewLazySystemDLL("kernel32.dll")
	procGetCurrentThreadId  = kernel32Dll.NewProc("GetCurrentThreadId")
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

	// 使用常规带顶层 HWND_TOP (0)，杜绝 TOPMOST 造成的 DWM 画面抽搐
	HWND_TOP       = uintptr(0)
	SWP_NOSIZE     = 0x0001
	SWP_NOMOVE     = 0x0002
	SWP_SHOWWINDOW = 0x0040

	VK_MENU         = 0x12   // Alt 键虚拟键码
	KEYEVENTF_KEYUP = 0x0002 // 释放按键标志
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

	// 1. 如果窗口最小化，先执行恢复
	if IsIconic(targetHwnd) {
		procShowWindow.Call(targetHwnd, SW_RESTORE)
	}

	// 2. 经典 Windows 前台锁穿透法：模拟触发一次微秒级的 Alt 键按下与抬起
	// 系统监测到 Alt 键动作时，会无条件开放前台抢占特权，彻底解除 SetForegroundWindow 的限制
	procKeybdEvent.Call(VK_MENU, 0, 0, 0)
	procKeybdEvent.Call(VK_MENU, 0, KEYEVENTF_KEYUP, 0)

	// 3. 挂接前台线程保障焦点接管
	fgHwnd, _, _ := procGetForegroundWindow.Call()
	curThreadId, _, _ := procGetCurrentThreadId.Call()
	var fgThreadId uintptr
	if fgHwnd != 0 {
		fgThreadId, _, _ = procGetWindowThread.Call(fgHwnd, 0)
	}

	attached := false
	if fgThreadId != 0 && fgThreadId != curThreadId {
		r, _, _ := procAttachThreadInput.Call(curThreadId, fgThreadId, 1)
		attached = (r != 0)
	}
	if attached {
		defer procAttachThreadInput.Call(curThreadId, fgThreadId, 0)
	}

	// 4. 原子化置于普通图层最顶层（HWND_TOP），不再进行 Topmost/Notopmost 震荡
	procSetWindowPos.Call(targetHwnd, HWND_TOP, 0, 0, 0, 0, SWP_NOMOVE|SWP_NOSIZE|SWP_SHOWWINDOW)
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

func IsWindow(hwnd uintptr) bool {
	if hwnd == 0 {
		return false
	}
	ret, _, _ := procIsWindow.Call(hwnd)
	return ret != 0
}

func IsWindowAliveAndResponsive(hwnd uintptr) bool {
	if hwnd == 0 {
		return false
	}
	return IsWindow(hwnd) && IsWindowVisible(hwnd) && !IsWindowHung(hwnd)
}
