package ui

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	MB_OK            = 0x00000000
	MB_OKCANCEL      = 0x00000001
	MB_ICONWARNING   = 0x00000030
	MB_TOPMOST       = 0x00040000
	MB_SETFOREGROUND = 0x00010000
)

var (
	dlgModComdlg32          = windows.NewLazySystemDLL("comdlg32.dll")
	dlgProcGetOpenFileNameW = dlgModComdlg32.NewProc("GetOpenFileNameW")

	dlgModUser32                 = windows.NewLazySystemDLL("user32.dll")
	procGetForegroundWindow      = dlgModUser32.NewProc("GetForegroundWindow")
	procGetWindowThreadProcessId = dlgModUser32.NewProc("GetWindowThreadProcessId")

	dlgModKernel32          = windows.NewLazySystemDLL("kernel32.dll")
	procGetCurrentProcessId = dlgModKernel32.NewProc("GetCurrentProcessId")
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

func getSafeOwnerHWND() windows.HWND {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd != 0 {
		var activeWindowPID uint32
		procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&activeWindowPID)))

		currentPID, _, _ := procGetCurrentProcessId.Call()
		if activeWindowPID == uint32(currentPID) {
			return windows.HWND(hwnd)
		}
	}
	return 0
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

	ofn.HwndOwner = getSafeOwnerHWND()

	ret, _, _ := dlgProcGetOpenFileNameW.Call(uintptr(unsafe.Pointer(&ofn)))
	if ret != 0 {
		return windows.UTF16ToString(buf), true
	}
	return "", false
}

func ShowErrorMessage(title, message string) {
	titlePtr, _ := windows.UTF16PtrFromString(title)
	msgPtr, _ := windows.UTF16PtrFromString(message)
	const flags = MB_OK | MB_ICONWARNING | MB_TOPMOST | MB_SETFOREGROUND
	_, _ = windows.MessageBox(getSafeOwnerHWND(), msgPtr, titlePtr, uint32(flags))
}

func ShowConfirmMessage(title, message string) bool {
	titlePtr, _ := windows.UTF16PtrFromString(title)
	msgPtr, _ := windows.UTF16PtrFromString(message)
	const flags = MB_OKCANCEL | MB_ICONWARNING | MB_TOPMOST | MB_SETFOREGROUND
	ret, _ := windows.MessageBox(getSafeOwnerHWND(), msgPtr, titlePtr, uint32(flags))
	return ret == 1
}
