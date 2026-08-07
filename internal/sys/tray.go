//go:build windows

package sys

import (
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type HICON uintptr

type MenuItem struct {
	ID           uint32
	Text         string
	Checked      bool
	Disabled     bool
	IsSeparator  bool
	SubMenuItems []MenuItem
}

type TrayHost struct {
	hwnd            windows.HWND
	tooltip         string
	onLeftClick     func()
	onRightClick    func()
	onMenuItemClick func(id uint32)
	onThemeChange   func(isDark bool)

	iconCache map[int]HICON
	iconMu    sync.RWMutex
	currIcon  HICON

	isStopped bool
}

const (
	AppModeAuto       = -1
	AppModeDefault    = 0
	AppModeAllowDark  = 1
	AppModeForceDark  = 2
	AppModeForceLight = 3
)

var (
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	user32   = windows.NewLazySystemDLL("user32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")
	advapi32 = windows.NewLazySystemDLL("advapi32.dll")
	uxtheme  = windows.NewLazySystemDLL("uxtheme.dll")

	pGetModuleHandleW = kernel32.NewProc("GetModuleHandleW")
	pGetProcAddress   = kernel32.NewProc("GetProcAddress")

	pRegisterClassExW       = user32.NewProc("RegisterClassExW")
	pCreateWindowExW        = user32.NewProc("CreateWindowExW")
	pDestroyWindow          = user32.NewProc("DestroyWindow")
	pDefWindowProcW         = user32.NewProc("DefWindowProcW")
	pGetMessageW            = user32.NewProc("GetMessageW")
	pTranslateMessage       = user32.NewProc("TranslateMessage")
	pDispatchMessageW       = user32.NewProc("DispatchMessageW")
	pPostQuitMessage        = user32.NewProc("PostQuitMessage")
	pPostMessageW           = user32.NewProc("PostMessageW")
	pCreatePopupMenu        = user32.NewProc("CreatePopupMenu")
	pAppendMenuW            = user32.NewProc("AppendMenuW")
	pDestroyMenu            = user32.NewProc("DestroyMenu")
	pTrackPopupMenu         = user32.NewProc("TrackPopupMenu")
	pSetForegroundWindow    = user32.NewProc("SetForegroundWindow")
	pGetCursorPos           = user32.NewProc("GetCursorPos")
	pCreateIconFromResource = user32.NewProc("CreateIconFromResourceEx")
	pRegisterWindowMessageW = user32.NewProc("RegisterWindowMessageW")
	pGetSystemMetrics       = user32.NewProc("GetSystemMetrics")

	pShell_NotifyIconW = shell32.NewProc("Shell_NotifyIconW")

	pRegOpenKeyExW    = advapi32.NewProc("RegOpenKeyExW")
	pRegQueryValueExW = advapi32.NewProc("RegQueryValueExW")
	pRegCloseKey      = advapi32.NewProc("RegCloseKey")

	procFlushMenuThemes         *windows.LazyProc
	procSetPreferredAppModeAddr uintptr

	isWin101903OrGreater bool
	currentMenuAppMode   int = AppModeAuto
)

func init() {
	maj, _, build := windows.RtlGetNtVersionNumbers()
	if maj > 10 || (maj == 10 && build >= 18362) {
		isWin101903OrGreater = true
		procFlushMenuThemes = uxtheme.NewProc("FlushMenuThemes")

		if hUxTheme := uxtheme.Handle(); hUxTheme != 0 {
			addr, _, _ := pGetProcAddress.Call(hUxTheme, 135)
			procSetPreferredAppModeAddr = addr
		}
	}
}

const (
	WM_USER           = 0x0400
	WM_TRAYICON       = WM_USER + 1
	WM_LBUTTONUP      = 0x0202
	WM_RBUTTONUP      = 0x0205
	WM_DESTROY        = 0x0002
	WM_SETTINGCHANGE  = 0x001A
	WM_POWERBROADCAST = 0x0218

	PBT_APMRESUMEAUTOMATIC = 0x0012
	SM_CXSMICON            = 49

	NIM_ADD    = 0x00000000
	NIM_MODIFY = 0x00000001
	NIM_DELETE = 0x00000002

	NIF_MESSAGE = 0x00000001
	NIF_ICON    = 0x00000002
	NIF_TIP     = 0x00000004

	MF_STRING    = 0x00000000
	MF_GRAYED    = 0x00000001
	MF_CHECKED   = 0x00000008
	MF_POPUP     = 0x00000010
	MF_SEPARATOR = 0x00000800

	TPM_LEFTALIGN   = 0x0000
	TPM_RIGHTBUTTON = 0x0002
	TPM_RETURNCMD   = 0x0100
	TPM_NONOTIFY    = 0x0080

	HKEY_CURRENT_USER = 0x80000001
	KEY_READ          = 0x20019
)

type NOTIFYICONDATAW struct {
	CbSize            uint32
	HWnd              windows.HWND
	UID               uint32
	UFlags            uint32
	UCallbackMessage  uint32
	HIcon             HICON
	SzTip             [128]uint16
	DwState           uint32
	DwStateMask       uint32
	SzInfo            [256]uint16
	UTimeoutOrVersion uint32
	SzInfoTitle       [64]uint16
	DwInfoFlags       uint32
	GuidItem          windows.GUID
	HBalloonIcon      HICON
}

type POINT struct {
	X int32
	Y int32
}

type WNDCLASSEXW struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     windows.Handle
	HIcon         HICON
	HCursor       windows.Handle
	HbrBackground windows.Handle
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       HICON
}

var (
	trayInstances     sync.Map
	msgTaskbarCreated uint32
)

func SetMenuTheme(mode int) {
	currentMenuAppMode = mode
	applyMenuTheme(mode)
}

func applyMenuTheme(mode int) {
	if !isWin101903OrGreater {
		return
	}

	targetMode := mode
	if targetMode == AppModeAuto {
		if IsDarkTheme() {
			targetMode = AppModeForceDark
		} else {
			targetMode = AppModeForceLight
		}
	}

	if procSetPreferredAppModeAddr != 0 {
		syscall.Syscall(procSetPreferredAppModeAddr, 1, uintptr(targetMode), 0, 0)
	}
	if procFlushMenuThemes != nil && procFlushMenuThemes.Find() == nil {
		procFlushMenuThemes.Call()
	}
}

func IsDarkTheme() bool {
	if !isWin101903OrGreater {
		return false
	}

	var hKey uintptr
	subKey, _ := windows.UTF16PtrFromString(`Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`)
	r, _, _ := pRegOpenKeyExW.Call(
		HKEY_CURRENT_USER,
		uintptr(unsafe.Pointer(subKey)),
		0,
		KEY_READ,
		uintptr(unsafe.Pointer(&hKey)),
	)
	if r != 0 {
		return false
	}
	defer pRegCloseKey.Call(hKey)

	var value uint32
	var valType uint32
	var bufSize = uint32(unsafe.Sizeof(value))
	valName, _ := windows.UTF16PtrFromString("SystemUsesLightTheme")

	r, _, _ = pRegQueryValueExW.Call(
		hKey,
		uintptr(unsafe.Pointer(valName)),
		0,
		uintptr(unsafe.Pointer(&valType)),
		uintptr(unsafe.Pointer(&value)),
		uintptr(unsafe.Pointer(&bufSize)),
	)

	if r != 0 {
		valNameApps, _ := windows.UTF16PtrFromString("AppsUseLightTheme")
		r, _, _ = pRegQueryValueExW.Call(
			hKey,
			uintptr(unsafe.Pointer(valNameApps)),
			0,
			uintptr(unsafe.Pointer(&valType)),
			uintptr(unsafe.Pointer(&value)),
			uintptr(unsafe.Pointer(&bufSize)),
		)
		if r != 0 {
			return false
		}
	}

	return value == 0
}

func initTaskbarMessage() {
	if msgTaskbarCreated == 0 {
		taskbarStr, _ := windows.UTF16PtrFromString("TaskbarCreated")
		r, _, _ := pRegisterWindowMessageW.Call(uintptr(unsafe.Pointer(taskbarStr)))
		msgTaskbarCreated = uint32(r)
	}
}

func wndProc(hwnd windows.HWND, msg uint32, wParam uintptr, lParam uintptr) uintptr {
	if msgTaskbarCreated != 0 && msg == msgTaskbarCreated {
		if host, ok := trayInstances.Load(hwnd); ok {
			th := host.(*TrayHost)
			th.addNotifyIcon()
		}
		return 0
	}

	switch msg {
	case WM_SETTINGCHANGE:
		if lParam != 0 {
			ptr := (*uint16)(unsafe.Pointer(lParam))
			str := windows.UTF16PtrToString(ptr)
			if str == "ImmersiveColorSet" {
				if host, ok := trayInstances.Load(hwnd); ok {
					th := host.(*TrayHost)
					isDark := IsDarkTheme()
					if th.onThemeChange != nil {
						th.onThemeChange(isDark)
					}
					th.updateNotifyIcon()
				}
			}
		}
		return 0

	case WM_POWERBROADCAST:
		if wParam == PBT_APMRESUMEAUTOMATIC {
			if host, ok := trayInstances.Load(hwnd); ok {
				th := host.(*TrayHost)
				go func() {
					time.Sleep(1 * time.Second)
					th.addNotifyIcon()
				}()
			}
		}
		return 0

	case WM_TRAYICON:
		switch uint32(lParam) {
		case WM_LBUTTONUP:
			if host, ok := trayInstances.Load(hwnd); ok {
				th := host.(*TrayHost)
				if th.onLeftClick != nil {
					th.onLeftClick()
				}
			}
		case WM_RBUTTONUP:
			if host, ok := trayInstances.Load(hwnd); ok {
				th := host.(*TrayHost)
				if th.onRightClick != nil {
					th.onRightClick()
				}
			}
		}
		return 0

	case WM_DESTROY:
		pPostQuitMessage.Call(0)
		return 0
	}

	r, _, _ := pDefWindowProcW.Call(uintptr(hwnd), uintptr(msg), wParam, lParam)
	return r
}

func NewTrayHost(tooltip string, onLeftClick, onRightClick func(), onMenuItemClick func(id uint32)) *TrayHost {
	th := &TrayHost{
		tooltip:         tooltip,
		onLeftClick:     onLeftClick,
		onRightClick:    onRightClick,
		onMenuItemClick: onMenuItemClick,
		iconCache:       make(map[int]HICON),
	}
	th.createWindow()
	return th
}

func (th *TrayHost) SetOnThemeChange(f func(isDark bool)) {
	th.onThemeChange = f
}

func (th *TrayHost) createWindow() {
	initTaskbarMessage()

	className, _ := windows.UTF16PtrFromString("MihomoTrayWindowClass")
	hInstance, _, _ := pGetModuleHandleW.Call(0)

	var wc WNDCLASSEXW
	wc.CbSize = uint32(unsafe.Sizeof(wc))
	wc.LpfnWndProc = syscall.NewCallback(wndProc)
	wc.HInstance = windows.Handle(hInstance)
	wc.LpszClassName = className

	pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))

	hwnd, _, _ := pCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(className)),
		0, 0, 0, 0, 0,
		0, 0,
		hInstance,
		0,
	)

	th.hwnd = windows.HWND(hwnd)
	trayInstances.Store(th.hwnd, th)
	th.addNotifyIcon()
}

func (th *TrayHost) RunMessageLoop() {
	var msg struct {
		HWnd    windows.HWND
		Message uint32
		WParam  uintptr
		LParam  uintptr
		Time    uint32
		Pt      POINT
	}

	for {
		r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		pDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

func (th *TrayHost) CacheIcon(id int, icoBytes []byte) {
	hIcon := bytesToHIcon(icoBytes)
	if hIcon != 0 {
		th.iconMu.Lock()
		th.iconCache[id] = hIcon
		th.iconMu.Unlock()
	}
}

func (th *TrayHost) SetIcon(id int) {
	th.iconMu.RLock()
	hIcon, exists := th.iconCache[id]
	th.iconMu.RUnlock()

	if exists && hIcon != th.currIcon {
		th.currIcon = hIcon
		th.updateNotifyIcon()
	}
}

func (th *TrayHost) ShowContextMenu(items []MenuItem) {
	applyMenuTheme(currentMenuAppMode)

	hMenu, _, _ := pCreatePopupMenu.Call()
	if hMenu == 0 {
		return
	}
	defer pDestroyMenu.Call(hMenu)

	buildMenu(hMenu, items)

	var pt POINT
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))

	pSetForegroundWindow.Call(uintptr(th.hwnd))

	flags := uintptr(TPM_LEFTALIGN | TPM_RIGHTBUTTON | TPM_RETURNCMD | TPM_NONOTIFY)
	r, _, _ := pTrackPopupMenu.Call(
		hMenu,
		flags,
		uintptr(pt.X),
		uintptr(pt.Y),
		0,
		uintptr(th.hwnd),
		0,
	)

	pPostMessageW.Call(uintptr(th.hwnd), 0, 0, 0)

	if r != 0 {
		selectedID := uint32(r)
		if th.onMenuItemClick != nil {
			th.onMenuItemClick(selectedID)
		}
	}
}

func buildMenu(hMenu uintptr, items []MenuItem) {
	for _, item := range items {
		if item.IsSeparator {
			pAppendMenuW.Call(hMenu, MF_SEPARATOR, 0, 0)
			continue
		}

		var flags uintptr = MF_STRING
		if item.Disabled {
			flags |= MF_GRAYED
		}
		if item.Checked {
			flags |= MF_CHECKED
		}

		textPtr, _ := windows.UTF16PtrFromString(item.Text)

		if len(item.SubMenuItems) > 0 {
			hSubMenu, _, _ := pCreatePopupMenu.Call()
			buildMenu(hSubMenu, item.SubMenuItems)
			flags |= MF_POPUP
			pAppendMenuW.Call(hMenu, flags, hSubMenu, uintptr(unsafe.Pointer(textPtr)))
		} else {
			pAppendMenuW.Call(hMenu, flags, uintptr(item.ID), uintptr(unsafe.Pointer(textPtr)))
		}
	}
}

func (th *TrayHost) Stop() {
	th.iconMu.Lock()
	if th.isStopped {
		th.iconMu.Unlock()
		return
	}
	th.isStopped = true
	th.iconMu.Unlock()

	th.removeNotifyIcon()
	if th.hwnd != 0 {
		trayInstances.Delete(th.hwnd)
		pDestroyWindow.Call(uintptr(th.hwnd))
	}
}

func (th *TrayHost) addNotifyIcon() {
	var nid NOTIFYICONDATAW
	nid.CbSize = uint32(unsafe.Sizeof(nid))
	nid.HWnd = th.hwnd
	nid.UID = 1
	nid.UFlags = NIF_MESSAGE | NIF_ICON | NIF_TIP
	nid.UCallbackMessage = WM_TRAYICON
	nid.HIcon = th.currIcon

	tip, _ := windows.UTF16FromString(th.tooltip)
	copy(nid.SzTip[:], tip)

	pShell_NotifyIconW.Call(NIM_ADD, uintptr(unsafe.Pointer(&nid)))
}

func (th *TrayHost) updateNotifyIcon() {
	var nid NOTIFYICONDATAW
	nid.CbSize = uint32(unsafe.Sizeof(nid))
	nid.HWnd = th.hwnd
	nid.UID = 1
	nid.UFlags = NIF_ICON
	nid.HIcon = th.currIcon

	r, _, _ := pShell_NotifyIconW.Call(NIM_MODIFY, uintptr(unsafe.Pointer(&nid)))
	if r == 0 {
		th.addNotifyIcon()
	}
}

func (th *TrayHost) removeNotifyIcon() {
	var nid NOTIFYICONDATAW
	nid.CbSize = uint32(unsafe.Sizeof(nid))
	nid.HWnd = th.hwnd
	nid.UID = 1

	pShell_NotifyIconW.Call(NIM_DELETE, uintptr(unsafe.Pointer(&nid)))
}

func bytesToHIcon(data []byte) HICON {
	if len(data) < 6 {
		return 0
	}
	idType := *(*uint16)(unsafe.Pointer(&data[2]))
	idCount := *(*uint16)(unsafe.Pointer(&data[4]))

	if idType != 1 || idCount == 0 {
		return 0
	}

	targetSize := uint32(16)
	if r, _, _ := pGetSystemMetrics.Call(SM_CXSMICON); r != 0 {
		targetSize = uint32(r)
	}

	var bestOffset uint32
	var bestSize uint32
	var bestWidth uint32

	for i := 0; i < int(idCount); i++ {
		entryOffset := 6 + i*16
		if entryOffset+16 > len(data) {
			break
		}

		w := uint32(data[entryOffset])
		if w == 0 {
			w = 256
		}

		dwBytesInRes := *(*uint32)(unsafe.Pointer(&data[entryOffset+8]))
		dwImageOffset := *(*uint32)(unsafe.Pointer(&data[entryOffset+12]))

		if int(dwImageOffset+dwBytesInRes) <= len(data) {
			if bestSize == 0 {
				bestOffset = dwImageOffset
				bestSize = dwBytesInRes
				bestWidth = w
				continue
			}

			if w >= targetSize {
				if bestWidth < targetSize || w < bestWidth {
					bestOffset = dwImageOffset
					bestSize = dwBytesInRes
					bestWidth = w
				}
			} else if bestWidth < targetSize && w > bestWidth {
				bestOffset = dwImageOffset
				bestSize = dwBytesInRes
				bestWidth = w
			}
		}
	}

	if bestSize == 0 {
		return 0
	}

	imgData := data[bestOffset : bestOffset+bestSize]

	r1, _, _ := pCreateIconFromResource.Call(
		uintptr(unsafe.Pointer(&imgData[0])),
		uintptr(len(imgData)),
		1,
		0x00030000,
		0, 0,
		0,
	)

	return HICON(r1)
}
