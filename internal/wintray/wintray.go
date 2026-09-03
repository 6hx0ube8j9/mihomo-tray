package wintray

import (
	"encoding/binary"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modKernel32 = windows.NewLazySystemDLL("kernel32.dll")
	modUser32   = windows.NewLazySystemDLL("user32.dll")
	modShell32  = windows.NewLazySystemDLL("shell32.dll")

	pGetModuleHandleW         = modKernel32.NewProc("GetModuleHandleW")
	pRegisterClassExW         = modUser32.NewProc("RegisterClassExW")
	pCreateWindowExW          = modUser32.NewProc("CreateWindowExW")
	pDefWindowProcW           = modUser32.NewProc("DefWindowProcW")
	pGetMessageW              = modUser32.NewProc("GetMessageW")
	pTranslateMessage         = modUser32.NewProc("TranslateMessage")
	pDispatchMessageW         = modUser32.NewProc("DispatchMessageW")
	pPostQuitMessage          = modUser32.NewProc("PostQuitMessage")
	pPostMessageW             = modUser32.NewProc("PostMessageW")
	pRegisterWindowMessageW   = modUser32.NewProc("RegisterWindowMessageW")
	pCreatePopupMenu          = modUser32.NewProc("CreatePopupMenu")
	pAppendMenuW              = modUser32.NewProc("AppendMenuW")
	pDestroyMenu              = modUser32.NewProc("DestroyMenu")
	pTrackPopupMenu           = modUser32.NewProc("TrackPopupMenu")
	pGetCursorPos             = modUser32.NewProc("GetCursorPos")
	pDestroyIcon              = modUser32.NewProc("DestroyIcon")
	pCreateIconFromResourceEx = modUser32.NewProc("CreateIconFromResourceEx")
	pGetSystemMetrics         = modUser32.NewProc("GetSystemMetrics")
	pSetForegroundWindow      = modUser32.NewProc("SetForegroundWindow")
	pShell_NotifyIconW        = modShell32.NewProc("Shell_NotifyIconW")
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

	isStopped bool
	ready     chan struct{}
}

const (
	WM_USER      = 0x0400
	WM_TRAYICON  = WM_USER + 1
	WM_LBUTTONUP = 0x0202
	WM_RBUTTONUP = 0x0205
	WM_DESTROY   = 0x0002
	WM_CLOSE     = 0x0010

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
	trayInstances      sync.Map
	wmTaskbarCreated   uint32
	wndProcCallbackPtr uintptr
	registerClassOnce  sync.Once
)

func init() {
	wndProcCallbackPtr = syscall.NewCallback(wndProc)
}

func wndProc(hwnd windows.HWND, msg uint32, wParam uintptr, lParam uintptr) uintptr {
	var th *TrayHost
	if host, ok := trayInstances.Load(hwnd); ok {
		th = host.(*TrayHost)
	}

	if wmTaskbarCreated != 0 && msg == wmTaskbarCreated {
		if th != nil {
			th.addNotifyIcon()
		}
		return 0
	}

	switch msg {
	case WM_TRAYICON:
		if th != nil {
			switch uint32(lParam) {
			case WM_LBUTTONUP:
				if th.onLeftClick != nil {
					th.onLeftClick()
				}
			case WM_RBUTTONUP:
				if th.onRightClick != nil {
					th.onRightClick()
				}
			}
		}
		return 0

	case WM_DESTROY:
		if th != nil {
			th.removeNotifyIcon()
			th.freeIcons()
			trayInstances.Delete(hwnd)
		}
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

	return &TrayHost{
		tooltip:         tooltip,
		onLeftClick:     onLeftClick,
		onRightClick:    onRightClick,
		onMenuItemClick: onMenuItemClick,
		iconCache:       make(map[int]HICON),
		ready:           make(chan struct{}),
	}
}

func (th *TrayHost) createWindow() {
	className, _ := windows.UTF16PtrFromString("WintrayWindowClass")
	hInstance, _, _ := pGetModuleHandleW.Call(0)

	registerClassOnce.Do(func() {
		var wc WNDCLASSEXW
		wc.CbSize = uint32(unsafe.Sizeof(wc))
		wc.LpfnWndProc = wndProcCallbackPtr
		wc.HInstance = windows.Handle(hInstance)
		wc.LpszClassName = className
		pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	})

	hwnd, _, _ := pCreateWindowExW.Call(
		0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(className)),
		0, 0, 0, 0, 0, 0, 0, hInstance, 0,
	)

	th.hwnd = windows.HWND(hwnd)
	trayInstances.Store(th.hwnd, th)
	th.addNotifyIcon()
	close(th.ready)
}

func (th *TrayHost) RunMessageLoop() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	th.createWindow()

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
	hIcon := loadBestFitIcon(icoBytes)
	if hIcon == 0 {
		return
	}

	var oldIcon HICON
	needUpdate := false

	th.iconMu.Lock()
	if old, exists := th.iconCache[id]; exists && old != 0 {
		oldIcon = old
		if oldIcon == th.currIcon {
			th.currIcon = hIcon
			needUpdate = true
		}
	}
	th.iconCache[id] = hIcon
	th.iconMu.Unlock()

	if needUpdate {
		th.updateNotifyIcon()
	}
	if oldIcon != 0 {
		pDestroyIcon.Call(uintptr(oldIcon))
	}
}

func (th *TrayHost) SetIcon(id int) {
	th.iconMu.Lock()
	hIcon, exists := th.iconCache[id]
	if !exists || hIcon == th.currIcon {
		th.iconMu.Unlock()
		return
	}
	th.currIcon = hIcon
	th.iconMu.Unlock()

	th.updateNotifyIcon()
}

func (th *TrayHost) ShowContextMenu(items []MenuItem) {
	<-th.ready

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
	r, _, _ := pTrackPopupMenu.Call(hMenu, flags, uintptr(pt.X), uintptr(pt.Y), 0, uintptr(th.hwnd), 0)
	pPostMessageW.Call(uintptr(th.hwnd), 0, 0, 0)

	if r != 0 {
		if th.onMenuItemClick != nil {
			th.onMenuItemClick(uint32(r))
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

	<-th.ready
	if th.hwnd != 0 {
		pPostMessageW.Call(uintptr(th.hwnd), WM_CLOSE, 0, 0)
	}
}

func (th *TrayHost) freeIcons() {
	th.iconMu.Lock()
	defer th.iconMu.Unlock()

	for id, hIcon := range th.iconCache {
		if hIcon != 0 {
			pDestroyIcon.Call(uintptr(hIcon))
		}
		delete(th.iconCache, id)
	}
	th.currIcon = 0
}

func (th *TrayHost) addNotifyIcon() {
	th.iconMu.RLock()
	currIcon := th.currIcon
	th.iconMu.RUnlock()

	var nid NOTIFYICONDATAW
	nid.CbSize = uint32(unsafe.Sizeof(nid))
	nid.HWnd = th.hwnd
	nid.UID = 1
	nid.UFlags = NIF_MESSAGE | NIF_ICON | NIF_TIP
	nid.UCallbackMessage = WM_TRAYICON
	nid.HIcon = currIcon

	tip, _ := windows.UTF16FromString(th.tooltip)
	copy(nid.SzTip[:], tip)
	nid.SzTip[len(nid.SzTip)-1] = 0

	pShell_NotifyIconW.Call(NIM_ADD, uintptr(unsafe.Pointer(&nid)))
}

func (th *TrayHost) updateNotifyIcon() {
	th.iconMu.RLock()
	currIcon := th.currIcon
	th.iconMu.RUnlock()

	var nid NOTIFYICONDATAW
	nid.CbSize = uint32(unsafe.Sizeof(nid))
	nid.HWnd = th.hwnd
	nid.UID = 1
	nid.UFlags = NIF_ICON
	nid.HIcon = currIcon

	pShell_NotifyIconW.Call(NIM_MODIFY, uintptr(unsafe.Pointer(&nid)))
}

func (th *TrayHost) removeNotifyIcon() {
	var nid NOTIFYICONDATAW
	nid.CbSize = uint32(unsafe.Sizeof(nid))
	nid.HWnd = th.hwnd
	nid.UID = 1

	pShell_NotifyIconW.Call(NIM_DELETE, uintptr(unsafe.Pointer(&nid)))
}

func loadBestFitIcon(data []byte) HICON {
	if len(data) < 6 {
		return 0
	}

	reserved := binary.LittleEndian.Uint16(data[0:2])
	icoType := binary.LittleEndian.Uint16(data[2:4])
	count := binary.LittleEndian.Uint16(data[4:6])

	if reserved != 0 || icoType != 1 || count == 0 {
		return 0
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
			return 0
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
		return 0
	}

	entryOffset := 6 + bestIndex*16
	bytesInRes := binary.LittleEndian.Uint32(data[entryOffset+8 : entryOffset+12])
	imageOffset := binary.LittleEndian.Uint32(data[entryOffset+12 : entryOffset+16])

	if uint64(imageOffset)+uint64(bytesInRes) > uint64(len(data)) {
		return 0
	}

	imageData := data[imageOffset : imageOffset+bytesInRes]
	if len(imageData) == 0 {
		return 0
	}

	hIcon, _, _ := pCreateIconFromResourceEx.Call(
		uintptr(unsafe.Pointer(&imageData[0])),
		uintptr(len(imageData)), 1, 0x00030000,
		uintptr(targetW), uintptr(targetH), 0,
	)

	return HICON(hIcon)
}

func abs32(n int32) int32 {
	if n < 0 {
		return -n
	}
	return n
}
