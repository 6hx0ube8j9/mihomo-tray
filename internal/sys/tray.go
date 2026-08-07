package main

import (
	"encoding/binary"
	"errors"
	"runtime"
	"syscall"
	"unsafe"
)

const (
	WM_USER          = 0x0400
	WM_TRAYICON      = WM_USER + 1
	WM_COMMAND       = 0x0111
	WM_DESTROY       = 0x0002
	WM_RBUTTONUP     = 0x0205
	WM_LBUTTONDBLCLK = 0x0203

	NIM_ADD    = 0x00000000
	NIM_MODIFY = 0x00000001
	NIM_DELETE = 0x00000002

	NIF_MESSAGE = 0x00000001
	NIF_ICON    = 0x00000002
	NIF_TIP     = 0x00000004

	SM_CXSMICON = 49
	SM_CYSMICON = 50

	MF_STRING       = 0x00000000
	TPM_LEFTALIGN   = 0x0000
	TPM_RIGHTBUTTON = 0x0002
	TPM_NONOTIFY    = 0x0080
	TPM_RETURNCMD   = 0x0100
)

type NOTIFYICONDATAW struct {
	CbSize           uint32
	HWnd             syscall.Handle
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	HIcon            syscall.Handle
	SzTip            [128]uint16
	DwState          uint32
	DwStateMask      uint32
	SzInfo           [256]uint16
	UVersion         uint32
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
	GuidItem         [16]byte
	HBalloonIcon     syscall.Handle
}

var (
	user32  = syscall.NewLazyDLL("user32.dll")
	shell32 = syscall.NewLazyDLL("shell32.dll")

	procRegisterClassExW         = user32.NewProc("RegisterClassExW")
	procCreateWindowExW          = user32.NewProc("CreateWindowExW")
	procDefWindowProcW           = user32.NewProc("DefWindowProcW")
	procGetMessageW              = user32.NewProc("GetMessageW")
	procTranslateMessage         = user32.NewProc("TranslateMessage")
	procDispatchMessageW         = user32.NewProc("DispatchMessageW")
	procPostQuitMessage          = user32.NewProc("PostQuitMessage")
	procDestroyWindow            = user32.NewProc("DestroyWindow")
	procRegisterWindowMessageW   = user32.NewProc("RegisterWindowMessageW")
	procGetSystemMetrics         = user32.NewProc("GetSystemMetrics")
	procCreatePopupMenu          = user32.NewProc("CreatePopupMenu")
	procAppendMenuW              = user32.NewProc("AppendMenuW")
	procTrackPopupMenu           = user32.NewProc("TrackPopupMenu")
	procDestroyMenu              = user32.NewProc("DestroyMenu")
	procSetForegroundWindow      = user32.NewProc("SetForegroundWindow")
	procGetCursorPos             = user32.NewProc("GetCursorPos")
	procPostMessageW             = user32.NewProc("PostMessageW")
	procDestroyIcon              = user32.NewProc("DestroyIcon")
	procCreateIconFromResourceEx = user32.NewProc("CreateIconFromResourceEx")

	procShell_NotifyIconW = shell32.NewProc("Shell_NotifyIconW")
)

var (
	wmTaskbarCreated uint32
	globalTray       *TrayIcon
)

type TrayIcon struct {
	hwnd     syscall.Handle
	hIcon    syscall.Handle
	nid      NOTIFYICONDATAW
	icoBytes []byte
	title    string
	onExit   func()
}

var embeddedIcoData = []byte{
	0x00, 0x00, 0x01, 0x00, 0x01, 0x00, 0x10, 0x10, 0x00, 0x00, 0x01, 0x00, 0x20, 0x00, 0x68, 0x00,
	0x00, 0x00, 0x16, 0x00, 0x00, 0x00, 0x28, 0x00, 0x00, 0x00, 0x10, 0x00, 0x00, 0x00, 0x20, 0x00,
	0x00, 0x00, 0x01, 0x00, 0x20, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
}

func main() {
	runtime.LockOSThread()

	tray, err := NewTrayIcon("我的稳健程序", embeddedIcoData, func() {})
	if err != nil {
		return
	}

	tray.RunMessageLoop()
}

func NewTrayIcon(title string, icoBytes []byte, onExit func()) (*TrayIcon, error) {
	taskbarStrPtr, _ := syscall.UTF16PtrFromString("TaskbarCreated")
	msgId, _, _ := procRegisterWindowMessageW.Call(uintptr(unsafe.Pointer(taskbarStrPtr)))
	wmTaskbarCreated = uint32(msgId)

	t := &TrayIcon{
		title:    title,
		icoBytes: icoBytes,
		onExit:   onExit,
	}
	globalTray = t

	classNamePtr, _ := syscall.UTF16PtrFromString("RobustTrayWindowClass")

	type WNDCLASSEXW struct {
		CbSize        uint32
		Style         uint32
		LpfnWndProc   uintptr
		CbClsExtra    int32
		CbWndExtra    int32
		HInstance     syscall.Handle
		HIcon         syscall.Handle
		HCursor       syscall.Handle
		HbrBackground syscall.Handle
		LpszMenuName  *uint16
		LpszClassName *uint16
		HIconSm       syscall.Handle
	}

	wc := WNDCLASSEXW{
		CbSize:        uint32(unsafe.Sizeof(WNDCLASSEXW{})),
		LpfnWndProc:   syscall.NewCallback(wndProc),
		LpszClassName: classNamePtr,
	}

	procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))

	hwnd, _, _ := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(classNamePtr)),
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	)

	if hwnd == 0 {
		return nil, errors.New("创建 Win32 消息窗口失败")
	}
	t.hwnd = syscall.Handle(hwnd)

	hIcon, err := loadBestFitIcon(icoBytes)
	if err == nil {
		t.hIcon = hIcon
	}

	t.nid.CbSize = uint32(unsafe.Sizeof(t.nid))
	t.nid.HWnd = t.hwnd
	t.nid.UID = 1001
	t.nid.UFlags = NIF_MESSAGE | NIF_ICON | NIF_TIP
	t.nid.UCallbackMessage = WM_TRAYICON
	t.nid.HIcon = t.hIcon

	tipChars, _ := syscall.UTF16FromString(title)
	copy(t.nid.SzTip[:], tipChars)

	procShell_NotifyIconW.Call(NIM_ADD, uintptr(unsafe.Pointer(&t.nid)))

	return t, nil
}

func wndProc(hwnd syscall.Handle, msg uint32, wParam, lParam uintptr) uintptr {
	if wmTaskbarCreated != 0 && msg == wmTaskbarCreated {
		if globalTray != nil {
			procShell_NotifyIconW.Call(NIM_ADD, uintptr(unsafe.Pointer(&globalTray.nid)))
		}
		return 0
	}

	switch msg {
	case WM_TRAYICON:
		switch uint32(lParam) {
		case WM_RBUTTONUP:
			showContextMenu(hwnd)
		case WM_LBUTTONDBLCLK:
		}
		return 0

	case WM_COMMAND:
		if uint32(wParam&0xffff) == 101 {
			if globalTray != nil && globalTray.onExit != nil {
				globalTray.onExit()
			}
			if globalTray != nil {
				globalTray.Destroy()
			}
			procPostQuitMessage.Call(0)
		}
		return 0

	case WM_DESTROY:
		if globalTray != nil {
			globalTray.Destroy()
		}
		procPostQuitMessage.Call(0)
		return 0
	}

	ret, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(msg), wParam, lParam)
	return ret
}

func showContextMenu(hwnd syscall.Handle) {
	hMenu, _, _ := procCreatePopupMenu.Call()
	if hMenu == 0 {
		return
	}
	defer procDestroyMenu.Call(hMenu)

	exitText, _ := syscall.UTF16PtrFromString("退出程序")
	procAppendMenuW.Call(hMenu, MF_STRING, 101, uintptr(unsafe.Pointer(exitText)))

	procSetForegroundWindow.Call(uintptr(hwnd))

	type POINT struct {
		X, Y int32
	}
	var pt POINT
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))

	flags := uintptr(TPM_LEFTALIGN | TPM_RIGHTBUTTON | TPM_RETURNCMD | TPM_NONOTIFY)
	retID, _, _ := procTrackPopupMenu.Call(
		hMenu,
		flags,
		uintptr(pt.X),
		uintptr(pt.Y),
		0,
		uintptr(hwnd),
		0,
	)

	procPostMessageW.Call(uintptr(hwnd), 0, 0, 0)

	if retID == 101 {
		if globalTray != nil && globalTray.onExit != nil {
			globalTray.onExit()
		}
		if globalTray != nil {
			globalTray.Destroy()
		}
		procPostQuitMessage.Call(0)
	}
}

func (t *TrayIcon) RunMessageLoop() {
	type MSG struct {
		HWnd    syscall.Handle
		Message uint32
		WParam  uintptr
		LParam  uintptr
		Time    uint32
		Pt      struct{ X, Y int32 }
	}

	var msg MSG
	for {
		ret, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(ret) <= 0 {
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

func (t *TrayIcon) Destroy() {
	procShell_NotifyIconW.Call(NIM_DELETE, uintptr(unsafe.Pointer(&t.nid)))
	if t.hIcon != 0 {
		procDestroyIcon.Call(uintptr(t.hIcon))
		t.hIcon = 0
	}
	if t.hwnd != 0 {
		procDestroyWindow.Call(uintptr(t.hwnd))
		t.hwnd = 0
	}
}

func loadBestFitIcon(data []byte) (syscall.Handle, error) {
	if len(data) < 6 {
		return 0, errors.New("ICO 数据长度不足")
	}

	reserved := binary.LittleEndian.Uint16(data[0:2])
	icoType := binary.LittleEndian.Uint16(data[2:4])
	count := binary.LittleEndian.Uint16(data[4:6])

	if reserved != 0 || icoType != 1 || count == 0 {
		return 0, errors.New("非法的 ICO 格式头")
	}

	targetW, _, _ := procGetSystemMetrics.Call(SM_CXSMICON)
	targetH, _, _ := procGetSystemMetrics.Call(SM_CYSMICON)
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

	hIcon, _, _ := procCreateIconFromResourceEx.Call(
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

	return syscall.Handle(hIcon), nil
}

func abs32(n int32) int32 {
	if n < 0 {
		return -n
	}
	return n
}
