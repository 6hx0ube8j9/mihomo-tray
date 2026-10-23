package sys

import (
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modIphlpapi = windows.NewLazySystemDLL("iphlpapi.dll")

	// 窗口与工作区相关 API
	procEnumWindows          = modUser32.NewProc("EnumWindows")
	procGetClassName         = modUser32.NewProc("GetClassNameW")
	procIsWindowVisible      = modUser32.NewProc("IsWindowVisible")
	procGetWindowThread      = modUser32.NewProc("GetWindowThreadProcessId")
	procGetWindowText        = modUser32.NewProc("GetWindowTextW")
	procShowWindow           = modUser32.NewProc("ShowWindow")
	procSystemParametersInfo = modUser32.NewProc("SystemParametersInfoW")
	procSetProcessDpiContext = modUser32.NewProc("SetProcessDpiAwarenessContext")
	procSetProcessDPIAware   = modUser32.NewProc("SetProcessDPIAware")
	procGetSystemMetrics     = modUser32.NewProc("GetSystemMetrics")
	procSetForeground        = modUser32.NewProc("SetForegroundWindow")
	procGetWindow            = modUser32.NewProc("GetWindow")
	procIsIconic             = modUser32.NewProc("IsIconic")
	procSetWindowPos         = modUser32.NewProc("SetWindowPos")
	procKeybdEvent           = modUser32.NewProc("keybd_event")

	// 网络端口查询 API
	procGetExtendedTcpTable = modIphlpapi.NewProc("GetExtendedTcpTable")
)

const (
	SW_RESTORE      = 9
	SW_SHOW         = 5
	GW_OWNER        = 4
	SPI_GETWORKAREA = 0x0030
	SM_CXSCREEN     = 0
	SM_CYSCREEN     = 1

	dpiAwarenessPerMonitorV2 = uintptr(0xFFFFFFFC) // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 (-4)

	hwndTopMost   = ^uintptr(0) // (HWND)-1
	hwndNoTopMost = ^uintptr(1) // (HWND)-2
	swpNoMove     = 0x0002
	swpNoSize     = 0x0001
	swpShowWindow = 0x0040

	vkMenu         = 0x12 // VK_MENU (Alt 键)
	keyeventfKeyup = 0x0002

	afInet                   = 2  // AF_INET (IPv4)
	afInet6                  = 23 // AF_INET6 (IPv6)
	tcpTableOwnerPidListener = 3  // TCP_TABLE_OWNER_PID_LISTENER (仅监听端点)
	errorInsufficientBuffer  = 122
)

type WindowInfo struct {
	Hwnd      uintptr
	Pid       uint32
	ClassName string
	Title     string
}

// MIB_TCPROW_OWNER_PID (IPv4)
type mibTcpRowOwnerPid struct {
	dwState      uint32
	dwLocalAddr  uint32
	dwLocalPort  uint32
	dwRemoteAddr uint32
	dwRemotePort uint32
	dwOwningPid  uint32
}

// MIB_TCP6ROW_OWNER_PID (IPv6)
type mibTcp6RowOwnerPid struct {
	ucLocalAddr     [16]byte
	dwLocalScopeId  uint32
	dwLocalPort     uint32
	ucRemoteAddr    [16]byte
	dwRemoteScopeId uint32
	dwRemotePort    uint32
	dwState         uint32
	dwOwningPid     uint32
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

// GetDisplayWorkArea 获取主屏幕可用工作区（排除任务栏后的坐标与宽高）
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


func ClaimForegroundPrivilege() {
	runtime.LockOSThread()
	procKeybdEvent.Call(vkMenu, 0, 0, 0)
	procKeybdEvent.Call(vkMenu, 0, keyeventfKeyup, 0)
}

// FocusWindowSilky 穿透前台焦点锁定并将目标窗口平滑置顶
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

	procKeybdEvent.Call(vkMenu, 0, 0, 0)
	procKeybdEvent.Call(vkMenu, 0, keyeventfKeyup, 0)

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

// GetProcessIdByPort 纯 Win32 API 查询指定端口的监听进程 PID（支持 IPv4 / IPv6）
func GetProcessIdByPort(port string) uint32 {
	portNum, err := strconv.ParseUint(strings.TrimSpace(port), 10, 16)
	if err != nil || portNum == 0 {
		return 0
	}
	targetPort := uint16(portNum)

	if pid := findTcp4ListenerPid(targetPort); pid != 0 {
		return pid
	}

	return findTcp6ListenerPid(targetPort)
}

func findTcp4ListenerPid(targetPort uint16) uint32 {
	buf, ok := queryExtendedTcpTable(afInet)
	if !ok || len(buf) < 4 {
		return 0
	}

	numEntries := *(*uint32)(unsafe.Pointer(&buf[0]))
	if numEntries == 0 {
		return 0
	}

	rowSize := unsafe.Sizeof(mibTcpRowOwnerPid{})
	if uintptr(numEntries) > (uintptr(len(buf))-4)/rowSize {
		return 0
	}

	rows := unsafe.Slice((*mibTcpRowOwnerPid)(unsafe.Pointer(&buf[4])), numEntries)
	for i := range rows {
		port := ((rows[i].dwLocalPort & 0xFF) << 8) | ((rows[i].dwLocalPort >> 8) & 0xFF)
		if uint16(port) == targetPort {
			return rows[i].dwOwningPid
		}
	}
	return 0
}

func findTcp6ListenerPid(targetPort uint16) uint32 {
	buf, ok := queryExtendedTcpTable(afInet6)
	if !ok || len(buf) < 4 {
		return 0
	}

	numEntries := *(*uint32)(unsafe.Pointer(&buf[0]))
	if numEntries == 0 {
		return 0
	}

	rowSize := unsafe.Sizeof(mibTcp6RowOwnerPid{})
	if uintptr(numEntries) > (uintptr(len(buf))-4)/rowSize {
		return 0
	}

	rows := unsafe.Slice((*mibTcp6RowOwnerPid)(unsafe.Pointer(&buf[4])), numEntries)
	for i := range rows {
		port := ((rows[i].dwLocalPort & 0xFF) << 8) | ((rows[i].dwLocalPort >> 8) & 0xFF)
		if uint16(port) == targetPort {
			return rows[i].dwOwningPid
		}
	}
	return 0
}

func queryExtendedTcpTable(af uint32) ([]byte, bool) {
	var size uint32
	procGetExtendedTcpTable.Call(
		0,
		uintptr(unsafe.Pointer(&size)),
		0,
		uintptr(af),
		uintptr(tcpTableOwnerPidListener),
		0,
	)
	if size == 0 {
		return nil, false
	}

	for i := 0; i < 3; i++ {
		if size == 0 {
			return nil, false
		}
		buf := make([]byte, size)
		ret, _, _ := procGetExtendedTcpTable.Call(
			uintptr(unsafe.Pointer(&buf[0])),
			uintptr(unsafe.Pointer(&size)),
			0,
			uintptr(af),
			uintptr(tcpTableOwnerPidListener),
			0,
		)
		if ret == 0 {
			return buf, true
		}
		if ret != errorInsufficientBuffer {
			return nil, false
		}
	}
	return nil, false
}
