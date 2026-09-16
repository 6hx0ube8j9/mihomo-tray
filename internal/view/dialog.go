package view

import (
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	WM_COMMAND   = 0x0111
	WM_DESTROY   = 0x0002
	WM_SETFONT   = 0x0030
	CB_ADDSTRING = 0x0143
	CB_GETCURSEL = 0x0147
	CB_SETCURSEL = 0x014E

	DEFAULT_GUI_FONT = 17
	SM_CXSCREEN      = 0
	SM_CYSCREEN      = 1
)

var (
	dlgModComdlg32 = windows.NewLazySystemDLL("comdlg32.dll")
	dlgModUser32   = windows.NewLazySystemDLL("user32.dll")
	dlgModKernel32 = windows.NewLazySystemDLL("kernel32.dll")
	dlgModGdi32    = windows.NewLazySystemDLL("gdi32.dll")

	dlgProcGetOpenFileNameW = dlgModComdlg32.NewProc("GetOpenFileNameW")
	dlgProcRegisterClassExW = dlgModUser32.NewProc("RegisterClassExW")
	dlgProcCreateWindowExW  = dlgModUser32.NewProc("CreateWindowExW")
	dlgProcDefWindowProcW   = dlgModUser32.NewProc("DefWindowProcW")
	dlgProcGetMessageW      = dlgModUser32.NewProc("GetMessageW")
	dlgProcTranslateMessage = dlgModUser32.NewProc("TranslateMessage")
	dlgProcDispatchMessageW = dlgModUser32.NewProc("DispatchMessageW")
	dlgProcPostQuitMessage  = dlgModUser32.NewProc("PostQuitMessage")
	dlgProcGetWindowTextW   = dlgModUser32.NewProc("GetWindowTextW")
	dlgProcSendMessageW     = dlgModUser32.NewProc("SendMessageW")
	dlgProcDestroyWindow    = dlgModUser32.NewProc("DestroyWindow")
	dlgProcGetSystemMetrics = dlgModUser32.NewProc("GetSystemMetrics")
	dlgProcGetModuleHandleW = dlgModKernel32.NewProc("GetModuleHandleW")
	dlgProcGetStockObject   = dlgModGdi32.NewProc("GetStockObject")
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

type SubDialogResult struct {
	Name       string
	URL        string
	Interval   int
	AutoUpdate bool
	OK         bool
}

var (
	subDlgClassOnce sync.Once
	subDlgWndProc   uintptr
	dlgResult       SubDialogResult

	hName          windows.HWND
	hUrl           windows.HWND
	hIntervalCombo windows.HWND
)

func init() {
	subDlgWndProc = syscall.NewCallback(subDialogProc)
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

	ret, _, _ := dlgProcGetOpenFileNameW.Call(uintptr(unsafe.Pointer(&ofn)))
	if ret != 0 {
		return windows.UTF16ToString(buf), true
	}
	return "", false
}

func ShowErrorMessage(title, message string) {
	titlePtr, _ := windows.UTF16PtrFromString(title)
	msgPtr, _ := windows.UTF16PtrFromString(message)
	const flags = windows.MB_OK | windows.MB_ICONWARNING | windows.MB_TOPMOST
	_, _ = windows.MessageBox(0, msgPtr, titlePtr, uint32(flags))
}

func ShowConfirmMessage(title, message string) bool {
	titlePtr, _ := windows.UTF16PtrFromString(title)
	msgPtr, _ := windows.UTF16PtrFromString(message)
	const flags = windows.MB_OKCANCEL | windows.MB_ICONWARNING | windows.MB_TOPMOST
	ret, _ := windows.MessageBox(0, msgPtr, titlePtr, uint32(flags))
	return ret == 1
}

func ShowSubDialog(windowTitle, defName, defUrl string, defInterval int) SubDialogResult {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	dlgResult = SubDialogResult{OK: false}
	className, _ := windows.UTF16PtrFromString("MihomoSubDlgClass")
	hInstance, _, _ := dlgProcGetModuleHandleW.Call(0)

	subDlgClassOnce.Do(func() {
		type WNDCLASSEX struct {
			CbSize, Style          uint32
			LpfnWndProc            uintptr
			CbClsExtra, CbWndExtra int32
			HInstance, HIcon, HCursor, HbrBackground windows.Handle
			LpszMenuName, LpszClassName              *uint16
			HIconSm                                  windows.Handle
		}
		var wc WNDCLASSEX
		wc.CbSize = uint32(unsafe.Sizeof(wc))
		wc.LpfnWndProc = subDlgWndProc
		wc.HInstance = windows.Handle(hInstance)
		wc.HbrBackground = windows.Handle(5) // COLOR_WINDOW
		wc.LpszClassName = className
		dlgProcRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	})

	dialogWidth := int32(840)
	dialogHeight := int32(100)
	screenWidth, _, _ := dlgProcGetSystemMetrics.Call(SM_CXSCREEN)
	screenHeight, _, _ := dlgProcGetSystemMetrics.Call(SM_CYSCREEN)
	posX := (int32(screenWidth) - dialogWidth) / 2
	posY := (int32(screenHeight) - dialogHeight) / 2

	title, _ := windows.UTF16PtrFromString(windowTitle)

	hwnd, _, _ := dlgProcCreateWindowExW.Call(
		0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(title)),
		0x10C80000,
		uintptr(posX), uintptr(posY), uintptr(dialogWidth), uintptr(dialogHeight),
		0, 0, hInstance, 0,
	)

	hFont, _, _ := dlgProcGetStockObject.Call(DEFAULT_GUI_FONT)

	createControl := func(class, text string, style uint32, x, y, w, h int32, id uintptr) windows.HWND {
		cCls, _ := windows.UTF16PtrFromString(class)
		cTxt, _ := windows.UTF16PtrFromString(text)
		hCtrl, _, _ := dlgProcCreateWindowExW.Call(
			0, uintptr(unsafe.Pointer(cCls)), uintptr(unsafe.Pointer(cTxt)),
			uintptr(style|0x50000000),
			uintptr(x), uintptr(y), uintptr(w), uintptr(h), uintptr(hwnd), id, hInstance, 0,
		)
		dlgProcSendMessageW.Call(hCtrl, WM_SETFONT, hFont, 1)
		return windows.HWND(hCtrl)
	}

	createControl("STATIC", "配置名称:", 0, 15, 22, 60, 20, 0)
	hName = createControl("EDIT", defName, 0x00800000|0x00010000, 80, 20, 100, 22, 0)

	createControl("STATIC", "订阅链接:", 0, 195, 22, 60, 20, 0)
	hUrl = createControl("EDIT", defUrl, 0x00800000|0x00010000|0x0080, 260, 20, 220, 22, 0)

	createControl("STATIC", "更新频率:", 0, 495, 22, 60, 20, 0)
	
	hIntervalCombo = createControl("COMBOBOX", "", 0x00200003|0x00010000, 560, 20, 110, 150, 0)

	comboItems := []string{"每 1 天更新", "每 3 天更新", "每 5 天更新", "每 7 天更新", "每 14 天更新", "每 30 天更新", "禁用自动更新"}
	for _, item := range comboItems {
		ptr, _ := windows.UTF16PtrFromString(item)
		dlgProcSendMessageW.Call(uintptr(hIntervalCombo), CB_ADDSTRING, 0, uintptr(unsafe.Pointer(ptr)))
	}

	defaultSel := 1
	switch defInterval {
	case 1:
		defaultSel = 0
	case 3:
		defaultSel = 1
	case 5:
		defaultSel = 2
	case 7:
		defaultSel = 3
	case 14:
		defaultSel = 4
	case 30:
		defaultSel = 5
	case 0:
		defaultSel = 6
	}
	dlgProcSendMessageW.Call(uintptr(hIntervalCombo), CB_SETCURSEL, uintptr(defaultSel), 0)

	createControl("BUTTON", "确定", 0x00000001|0x00010000, 680, 19, 55, 24, 1)
	createControl("BUTTON", "取消", 0x00010000, 745, 19, 55, 24, 2)

	var msg struct {
		Hwnd    windows.HWND
		Message uint32
		WParam  uintptr
		LParam  uintptr
		Time    uint32
		Pt      struct{ X, Y int32 }
	}

	for {
		r, _, _ := dlgProcGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		dlgProcTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		dlgProcDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}

	return dlgResult
}

func subDialogProc(hwnd windows.HWND, msg uint32, wParam, lParam uintptr) uintptr {
	switch msg {
	case WM_COMMAND:
		id := wParam & 0xFFFF
		if id == 1 {
			buf := make([]uint16, 4096)

			dlgProcGetWindowTextW.Call(uintptr(hName), uintptr(unsafe.Pointer(&buf[0])), 4096)
			dlgResult.Name = windows.UTF16ToString(buf)

			dlgProcGetWindowTextW.Call(uintptr(hUrl), uintptr(unsafe.Pointer(&buf[0])), 4096)
			dlgResult.URL = windows.UTF16ToString(buf)

			idx, _, _ := dlgProcSendMessageW.Call(uintptr(hIntervalCombo), CB_GETCURSEL, 0, 0)

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
				dlgResult.Interval = 14
				dlgResult.AutoUpdate = true
			case 5:
				dlgResult.Interval = 30
				dlgResult.AutoUpdate = true
			case 6:
				dlgResult.Interval = 0
				dlgResult.AutoUpdate = false
			default:
				dlgResult.Interval = 3
				dlgResult.AutoUpdate = true
			}

			dlgResult.OK = true
			dlgProcDestroyWindow.Call(uintptr(hwnd))
		} else if id == 2 {
			dlgResult.OK = false
			dlgProcDestroyWindow.Call(uintptr(hwnd))
		}
	case WM_DESTROY:
		dlgProcPostQuitMessage.Call(0)
		return 0
	}
	ret, _, _ := dlgProcDefWindowProcW.Call(uintptr(hwnd), uintptr(msg), wParam, lParam)
	return ret
}
