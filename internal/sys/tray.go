package sys

import (
	"encoding/binary"
	"errors"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type HICON windows.Handle

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

	iconCache map[int]HICON
	iconMu    sync.RWMutex
	currIcon  HICON

	nid       NOTIFYICONDATAW
	isStopped bool
}

var (
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	user32   = windows.NewLazySystemDLL("user32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")

	pGetModuleHandleW          = kernel32.NewProc("GetModuleHandleW")
	pRegisterClassExW          = user32.NewProc("RegisterClassExW")
	pCreateWindowExW           = user32.NewProc("CreateWindowExW")
	pDestroyWindow             = user32.NewProc("DestroyWindow")
	pDefWindowProcW            = user32.NewProc("DefWindowProcW")
	pGetMessageW               = user32.NewProc("GetMessageW")
	pTranslateMessage          = user32.NewProc("TranslateMessage")
	pDispatchMessageW          = user32.NewProc("DispatchMessageW")
	pPostQuitMessage           = user32.NewProc("PostQuitMessage")
	pPostMessageW              = user32.NewProc("PostMessageW")
	pRegisterWindowMessageW    = user32.NewProc("RegisterWindowMessageW")
	pGetSystemMetrics          = user32.NewProc("GetSystemMetrics")
	pCreatePopupMenu           = user32.NewProc("CreatePopupMenu")
	pAppendMenuW               = user32.NewProc("AppendMenuW")
	pDestroyMenu               = user32.NewProc("DestroyMenu")
	pTrackPopupMenu            = user32.NewProc("TrackPopupMenu")
	pSetForegroundWindow       = user32.NewProc("SetForegroundWindow")
	pGetCursorPos              = user32.NewProc("GetCursorPos")
	pDestroyIcon               = user32.NewProc("DestroyIcon")
	pCreateIconFromResourceEx  = user32.NewProc("CreateIconFromResourceEx")

	pShell_NotifyIconW = shell32.NewProc("Shell_NotifyIconW")
)

const (
	WM_USER          = 0x0400
	WM_TRAYICON      = WM_USER + 1
	WM_LBUTTONUP     = 0x0202
	WM_RBUTTONUP     = 0x0205
	WM_DESTROY       = 0x0002

	NIM_ADD    = 0x00000000
	NIM_MODIFY = 0x00000001
	NIM_DELETE = 0x00000002

	NIF_MESSAGE = 0x00000001
	NIF_ICON    = 0x00000002
	NIF_TIP     = 0x00000004

	SM_CXSMICON = 49
	SM_CYSMICON = 50

	MF_STRING    = 0x00000000
	MF_GRAYED    = 0x00000001
	MF_CHECKED   = 0x00000008
	MF_POPUP     = 0x00000010
	MF_SEPARATOR = 0x00000800

	TPM_LEFTALIGN   = 0x0000
	TPM_RIGHTBUTTON = 0x0002
	TPM_RETURNCMD   = 0x0100
	TPM_NONOTIFY    = 0x0080
)

type NOTIFYICONDATAW struct {
	CbSize           uint32
	HWnd             windows.HWND
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	HIcon            HICON
	SzTip            [128]uint16
	DwState          uint32
	DwStateMask      uint32
	SzInfo           [256]uint16
	UTimeoutOrVersion uint32
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
	GuidItem         windows.GUID
	HBalloonIcon     HICON
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
	trayInstances    sync.Map
	wmTaskbarCreated uint32
)

func wndProc(hwnd windows.HWND, msg uint32, wParam uintptr, lParam uintptr) uintptr {
	if wmTaskbarCreated != 0 && msg == wmTaskbarCreated {
		if host, ok := trayInstances.Load(hwnd); ok {
			th := host.(*TrayHost)
			th.addNotifyIcon()
		}
		return 0
	}

	switch msg {
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
	taskbarStrPtr, _ := windows.UTF16PtrFromString("TaskbarCreated")
	msgId, _, _ := pRegisterWindowMessageW.Call(uintptr(unsafe.Pointer(taskbarStrPtr)))
	wmTaskbarCreated = uint32(msgId)

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

func (th *TrayHost) createWindow() {
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
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

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
	hIcon, err := loadBestFitIcon(icoBytes)
	if err != nil {
		return
	}

	th.iconMu.Lock()
	defer th.iconMu.Unlock()

	if oldIcon, exists := th.iconCache[id]; exists && oldIcon != 0 {
		pDestroyIcon.Call(uintptr(oldIcon))
	}

	th.iconCache[id] = hIcon
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

	for id, hIcon := range th.iconCache {
		if hIcon != 0 {
			pDestroyIcon.Call(uintptr(hIcon))
		}
		delete(th.iconCache, id)
	}
	th.currIcon = 0
	th.iconMu.Unlock()

	th.removeNotifyIcon()

	if th.hwnd != 0 {
		trayInstances.Delete(th.hwnd)
		pDestroyWindow.Call(uintptr(th.hwnd))
		th.hwnd = 0
	}
}

func (th *TrayHost) addNotifyIcon() {
	th.nid.CbSize = uint32(unsafe.Sizeof(th.nid))
	th.nid.HWnd = th.hwnd
	th.nid.UID = 1
	th.nid.UFlags = NIF_MESSAGE | NIF_ICON | NIF_TIP
	th.nid.UCallbackMessage = WM_TRAYICON
	th.nid.HIcon = th.currIcon

	tip, _ := windows.UTF16FromString(th.tooltip)
	copy(th.nid.SzTip[:], tip)

	pShell_NotifyIconW.Call(NIM_ADD, uintptr(unsafe.Pointer(&th.nid)))
}

func (th *TrayHost) updateNotifyIcon() {
	th.nid.CbSize = uint32(unsafe.Sizeof(th.nid))
	th.nid.HWnd = th.hwnd
	th.nid.UID = 1
	th.nid.UFlags = NIF_ICON
	th.nid.HIcon = th.currIcon

	pShell_NotifyIconW.Call(NIM_MODIFY, uintptr(unsafe.Pointer(&th.nid)))
}

func (th *TrayHost) removeNotifyIcon() {
	var nid NOTIFYICONDATAW
	nid.CbSize = uint32(unsafe.Sizeof(nid))
	nid.HWnd = th.hwnd
	nid.UID = 1

	pShell_NotifyIconW.Call(NIM_DELETE, uintptr(unsafe.Pointer(&nid)))
}

func loadBestFitIcon(data []byte) (HICON, error) {
	if len(data) < 6 {
		return 0, errors.New("ICO 数据长度不足")
	}

	reserved := binary.LittleEndian.Uint16(data[0:2])
	icoType := binary.LittleEndian.Uint16(data[2:4])
	count := binary.LittleEndian.Uint16(data[4:6])

	if reserved != 0 || icoType != 1 || count == 0 {
		return 0, errors.New("非法的 ICO 格式头")
	}

	targetW, _, _ := pGetSystemMetrics.Call(SM_CXSMICON)
	targetH, _, _ := pGetSystemMetrics.Call(SM_CYSMICON)
	if targetW == 0 {
		targetW = 16
	}
	if targetH == 0 {
		targetH = 16
	}

	bestIndex := -1
	bestDiff := int32(99999)

	for i := 0; i < int(count); i++ {
		entryOffset := 6 + i*16
		if entryOffset+16 > len(data) {
			return 0, errors.New("ICO 目录项数据越界")
		}

		w := int32(data[entryOffset])
		h := int32(data[entryOffset+1])
		if w == 0 {
			w = 256
		}
		if h == 0 {
			h = 256
		}

		diff := abs32(w-int32(targetW)) + abs32(h-int32(targetH))
		if diff < bestDiff {
			bestDiff = diff
			bestIndex = i
		}
	}

	if bestIndex == -1 {
		return 0, errors.New("未找到匹配的图标帧")
	}

	entryOffset := 6 + bestIndex*16
	bytesInRes := binary.LittleEndian.Uint32(data[entryOffset+8 : entryOffset+12])
	imageOffset := binary.LittleEndian.Uint32(data[entryOffset+12 : entryOffset+16])

	if uint64(imageOffset)+uint64(bytesInRes) > uint64(len(data)) {
		return 0, errors.New("ICO 图像偏移数据越界")
	}

	imageData := data[imageOffset : imageOffset+bytesInRes]

	hIcon, _, _ := pCreateIconFromResourceEx.Call(
		uintptr(unsafe.Pointer(&imageData[0])),
		uintptr(len(imageData)),
		1,
		0x00030000,
		uintptr(targetW),
		uintptr(targetH),
		0,
	)

	if hIcon == 0 {
		return 0, errors.New("CreateIconFromResourceEx 构建图标句柄失败")
	}

	return HICON(hIcon), nil
}

func abs32(n int32) int32 {
	if n < 0 {
		return -n
	}
	return n
}
