package ui

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"syscall"
	"unsafe"

	"github.com/tailscale/walk"
	"golang.org/x/sys/windows"
)

var (
	modOle32             = syscall.NewLazyDLL("ole32.dll")
	procCoCreateInstance = modOle32.NewProc("CoCreateInstance")
	procCoTaskMemFree    = modOle32.NewProc("CoTaskMemFree")

	clsidFileOpenDialog = windows.GUID{
		Data1: 0xDC1C5A9C, Data2: 0xE88A, Data3: 0x4DDE,
		Data4: [8]byte{0xA5, 0xA1, 0x60, 0xF8, 0x2A, 0x20, 0xAE, 0xF7},
	}
	iidIFileOpenDialog = windows.GUID{
		Data1: 0xD57C7288, Data2: 0xD4AD, Data3: 0x4768,
		Data4: [8]byte{0xBE, 0x02, 0x9D, 0x96, 0x95, 0x32, 0xD9, 0x60},
	}
)

type comFilterSpec struct {
	pszName *uint16
	pszSpec *uint16
}

type vtblObj struct {
	vtbl *[30]uintptr
}

func (obj *vtblObj) call(idx int, args ...uintptr) uintptr {
	allArgs := append([]uintptr{uintptr(unsafe.Pointer(obj))}, args...)
	ret, _, _ := syscall.SyscallN(obj.vtbl[idx], allArgs...)
	return ret
}

func RunOpenYAMLFileDialog(owner walk.Form) (string, bool) {
	var unknown *vtblObj
	hr, _, _ := procCoCreateInstance.Call(
		uintptr(unsafe.Pointer(&clsidFileOpenDialog)),
		0,
		windows.CLSCTX_INPROC_SERVER,
		uintptr(unsafe.Pointer(&iidIFileOpenDialog)),
		uintptr(unsafe.Pointer(&unknown)),
	)
	if hr != 0 || unknown == nil {
		slog.Warn("初始化 IFileOpenDialog COM 实例失败，回退至原生对话框", "hresult", fmt.Sprintf("0x%08X", hr))
		return fallbackWalkDialog(owner)
	}
	defer unknown.call(2) // Release

	unknown.call(17, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("导入本地配置"))))

	filters := []comFilterSpec{
		{
			pszName: windows.StringToUTF16Ptr("YAML 配置文件 (*.yaml;*.yml)"),
			pszSpec: windows.StringToUTF16Ptr("*.yaml;*.yml"),
		},
		{
			pszName: windows.StringToUTF16Ptr("所有文件 (*.*)"),
			pszSpec: windows.StringToUTF16Ptr("*.*"),
		},
	}
	unknown.call(4, uintptr(len(filters)), uintptr(unsafe.Pointer(&filters[0])))

	var currentOptions uint32
	unknown.call(10, uintptr(unsafe.Pointer(&currentOptions)))
	const options = 0x00000040 | 0x00001000 | 0x00000800 | 0x02000000
	unknown.call(9, uintptr(currentOptions|options))

	var ownerHwnd uintptr
	if owner != nil {
		ownerHwnd = uintptr(owner.Handle())
	}

	var showRet uintptr
	runWithCBTCenter(owner, nil, func() {
		showRet = unknown.call(3, ownerHwnd)
	})

	if showRet != 0 {
		return "", false
	}

	var shellItem *vtblObj
	if unknown.call(20, uintptr(unsafe.Pointer(&shellItem))) != 0 || shellItem == nil {
		return "", false
	}
	defer shellItem.call(2) // Release

	var pathPtr *uint16
	if shellItem.call(5, 0x80058000, uintptr(unsafe.Pointer(&pathPtr))) != 0 || pathPtr == nil {
		return "", false
	}
	defer procCoTaskMemFree.Call(uintptr(unsafe.Pointer(pathPtr)))

	return filepath.Clean(windows.UTF16PtrToString(pathPtr)), true
}

func fallbackWalkDialog(owner walk.Form) (string, bool) {
	dlg := new(walk.FileDialog)
	dlg.Title = "导入本地配置"
	dlg.Filter = "YAML 配置文件 (*.yaml;*.yml)|*.yaml;*.yml|所有文件 (*.*)|*.*"
	dlg.Flags = 0x00080000 // OFN_EXPLORER
	ok, _ := dlg.ShowOpen(owner)
	return dlg.FilePath, ok
}
