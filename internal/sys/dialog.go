package sys

import (
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)


var (
	modComdlg32          = windows.NewLazySystemDLL("comdlg32.dll")
	procGetOpenFileNameW = modComdlg32.NewProc("GetOpenFileNameW")
)

type OPENFILENAMEW struct {
	LStructSize       uint32
	HwndOwner         windows.HWND
	HInstance         windows.Handle
	LpstrFilter       *uint16
	LpstrCustomFilter *uint16
	NMaxCustFilter    uint32
	NFilterIndex      uint32
	LpstrFile         *uint16
	NMaxFile          uint32
	LpstrFileTitle    *uint16
	NMaxFileTitle     uint32
	LpstrInitialDir   *uint16
	LpstrTitle        *uint16
	Flags             uint32
	NFileOffset       uint16
	NFileExtension    uint16
	LpstrDefExt       *uint16
	LCustData         uintptr
	LpfnHook          uintptr
	LpTemplateName    *uint16
	PvReserved        uintptr
	DwReserved        uint32
	FlagsEx           uint32
}

func OpenYAMLFileDialog() (string, bool) {
	var ofn OPENFILENAMEW
	ofn.LStructSize = uint32(unsafe.Sizeof(ofn))

	filterStr := "YAML 配置文件 (*.yaml;*.yml)\x00*.yaml;*.yml\x00所有文件 (*.*)\x00*.*\x00\x00"
	filter, _ := windows.UTF16PtrFromString(filterStr)
	ofn.LpstrFilter = filter

	buf := make([]uint16, windows.MAX_PATH)
	ofn.LpstrFile = &buf[0]
	ofn.NMaxFile = windows.MAX_PATH

	title, _ := windows.UTF16PtrFromString("选择本地 YAML 配置文件")
	ofn.LpstrTitle = title

	ofn.Flags = 0x00001000 | 0x00000008 | 0x00000004

	ret, _, _ := procGetOpenFileNameW.Call(uintptr(unsafe.Pointer(&ofn)))
	if ret != 0 {
		return windows.UTF16ToString(buf), true
	}
	return "", false
}

func ShowErrorMessage(title, message string) {
	titlePtr, _ := windows.UTF16PtrFromString(title)
	msgPtr, _ := windows.UTF16PtrFromString(message)
	const flags = windows.MB_OK | windows.MB_ICONWARNING | windows.MB_TOPMOST
	windows.MessageBox(0, msgPtr, titlePtr, uint32(flags))
}


var (
	modUser32Dlg   = windows.NewLazySystemDLL("user32.dll")
	modKernel32Dlg = windows.NewLazySystemDLL("kernel32.dll")
	modGdi32Dlg    = windows.NewLazySystemDLL("gdi32.dll")

	procRegisterClassExW = modUser32Dlg.NewProc("RegisterClassExW")
	procCreateWindowExW  = modUser32Dlg.NewProc("CreateWindowExW")
	procDefWindowProcW   = modUser32Dlg.NewProc("DefWindowProcW")
	procGetMessageW      = modUser32Dlg.NewProc("GetMessageW")
	procTranslateMessage = modUser32Dlg.NewProc("TranslateMessage")
	procDispatchMessageW = modUser32Dlg.NewProc("DispatchMessageW")
	procPostQuitMessage  = modUser32Dlg.NewProc("PostQuitMessage")
	procGetWindowTextW   = modUser32Dlg.NewProc("GetWindowTextW")
	procSendMessageW     = modUser32Dlg.NewProc("SendMessageW")
	procDestroyWindow    = modUser32Dlg.NewProc("DestroyWindow")

	procGetModuleHandleW = modKernel32Dlg.NewProc("GetModuleHandleW")
	procGetStockObject   = modGdi32Dlg.NewProc("GetStockObject")
)

const (
	WM_COMMAND   = 0x0111
	WM_DESTROY   = 0x0002
	WM_SETFONT   = 0x0030
	CB_ADDSTRING = 0x0143
	CB_GETCURSEL = 0x0147
	CB_SETCURSEL = 0x014E
	
	DEFAULT_GUI_FONT = 17
)

type SubDialogResult struct {
	URL        string
	Name       string
	Interval   int
	AutoUpdate bool
	OK         bool
}

var (
	subDlgClassOnce sync.Once
	subDlgWndProc   uintptr
	dlgResult       SubDialogResult
	hUrl, hName     windows.HWND
	hIntervalCombo  windows.HWND
)

func init() {
	subDlgWndProc = syscall.NewCallback(subDialogProc)
}

func subDialogProc(hwnd windows.HWND, msg uint32, wParam, lParam uintptr) uintptr {
	switch msg {
	case WM_COMMAND:
		id := wParam & 0xFFFF
		if id == 1 { // 点击“确定”按钮
			buf := make([]uint16, 1024)
			
			procGetWindowTextW.Call(uintptr(hUrl), uintptr(unsafe.Pointer(&buf[0])), 1024)
			dlgResult.URL = windows.UTF16ToString(buf)

			procGetWindowTextW.Call(uintptr(hName), uintptr(unsafe.Pointer(&buf[0])), 1024)
			dlgResult.Name = windows.UTF16ToString(buf)

			idx, _, _ := procSendMessageW.Call(uintptr(hIntervalCombo), CB_GETCURSEL, 0, 0)
			
			// 下拉菜单索引映射：0:1天, 1:3天, 2:5天, 3:7天, 4:禁用自动更新
			switch idx {
			case 0:
				dlgResult.Interval = 1
				dlgResult.AutoUpdate = true
			case 1:
				dlgResult.Interval = 3
				dlgResult.AutoUpdate = true
			case 2:
				dlgResult.Interval = 5
				dlgResult.AutoUpdate = true
			case 3:
				dlgResult.Interval = 7
				dlgResult.AutoUpdate = true
			case 4:
				dlgResult.Interval = 0
				dlgResult.AutoUpdate = false
			default:
				dlgResult.Interval = 3
				dlgResult.AutoUpdate = true
			}
			
			dlgResult.OK = true
			procDestroyWindow.Call(uintptr(hwnd))
		} else if id == 2 { // 点击“取消”按钮
			dlgResult.OK = false
			procDestroyWindow.Call(uintptr(hwnd))
		}
	case WM_DESTROY:
		procPostQuitMessage.Call(0)
		return 0
	}
	ret, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(msg), wParam, lParam)
	return ret
}

func ShowAddSubDialog() SubDialogResult {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	dlgResult = SubDialogResult{OK: false}
	className, _ := windows.UTF16PtrFromString("MihomoSubDlgClass")
	hInstance, _, _ := procGetModuleHandleW.Call(0)

	subDlgClassOnce.Do(func() {
		type WNDCLASSEX struct {
			CbSize, Style uint32; LpfnWndProc uintptr; CbClsExtra, CbWndExtra int32
			HInstance, HIcon, HCursor, HbrBackground windows.Handle
			LpszMenuName, LpszClassName *uint16; HIconSm windows.Handle
		}
		var wc WNDCLASSEX
		wc.CbSize = uint32(unsafe.Sizeof(wc))
		wc.LpfnWndProc = subDlgWndProc
		wc.HInstance = windows.Handle(hInstance)
		wc.HbrBackground = windows.Handle(5) // COLOR_WINDOW
		wc.LpszClassName = className
		procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	})

	title, _ := windows.UTF16PtrFromString("添加远程订阅")
	// WS_VISIBLE | WS_SYSMENU | WS_CAPTION 
	hwnd, _, _ := procCreateWindowExW.Call(
		0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(title)),
		0x10C80000,
		windows.CW_USEDEFAULT, windows.CW_USEDEFAULT, 400, 240, 0, 0, hInstance, 0,
	)

	hFont, _, _ := procGetStockObject.Call(DEFAULT_GUI_FONT)

	createControl := func(class, text string, style uint32, x, y, w, h int32, id uintptr) windows.HWND {
		cCls, _ := windows.UTF16PtrFromString(class)
		cTxt, _ := windows.UTF16PtrFromString(text)
		hCtrl, _, _ := procCreateWindowExW.Call(
			0, uintptr(unsafe.Pointer(cCls)), uintptr(unsafe.Pointer(cTxt)),
			uintptr(style|0x50000000), // WS_CHILD | WS_VISIBLE
			uintptr(x), uintptr(y), uintptr(w), uintptr(h), uintptr(hwnd), id, hInstance, 0,
		)
		procSendMessageW.Call(hCtrl, WM_SETFONT, hFont, 1)
		return windows.HWND(hCtrl)
	}

	createControl("STATIC", "订阅链接:", 0, 20, 25, 70, 20, 0)
	hUrl = createControl("EDIT", "", 0x00800000|0x00010000, 100, 20, 260, 22, 0) // WS_BORDER | WS_TABSTOP

	createControl("STATIC", "订阅名称:", 0, 20, 65, 70, 20, 0)
	hName = createControl("EDIT", "", 0x00800000|0x00010000, 100, 60, 260, 22, 0) // WS_BORDER | WS_TABSTOP


	createControl("STATIC", "更新频率:", 0, 20, 105, 70, 20, 0)

	hIntervalCombo = createControl("COMBOBOX", "", 0x00200003|0x00010000, 100, 100, 260, 120, 0)

	comboItems := []string{"每 1 天更新", "每 3 天更新", "每 5 天更新", "每 7 天更新", "禁用自动更新"}
	for _, item := range comboItems {
		ptr, _ := windows.UTF16PtrFromString(item)
		procSendMessageW.Call(uintptr(hIntervalCombo), CB_ADDSTRING, 0, uintptr(unsafe.Pointer(ptr)))
	}
	
	procSendMessageW.Call(uintptr(hIntervalCombo), CB_SETCURSEL, 1, 0)

	createControl("BUTTON", "确定", 0x00000001|0x00010000, 200, 150, 75, 25, 1)
	createControl("BUTTON", "取消", 0x00010000, 285, 150, 75, 25, 2)

	var msg struct {
		Hwnd    windows.HWND
		Message uint32
		WParam  uintptr
		LParam  uintptr
		Time    uint32
		Pt      struct{ X, Y int32 }
	}
	
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
	
	return dlgResult
}
