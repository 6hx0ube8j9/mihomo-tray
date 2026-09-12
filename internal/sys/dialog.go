package sys

import (
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

	// OFN_FILEMUSTEXIST | OFN_NOCHANGEDIR | OFN_HIDEREADONLY
	ofn.Flags = 0x00001000 | 0x00000008 | 0x00000004

	ret, _, _ := procGetOpenFileNameW.Call(uintptr(unsafe.Pointer(&ofn)))
	if ret != 0 {
		return windows.UTF16ToString(buf), true
	}
	return "", false
}
