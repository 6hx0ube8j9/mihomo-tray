package sys

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procLoadImageW   = modUser32.NewProc("LoadImageW")
	procSendMessageW = modUser32.NewProc("SendMessageW")
)

const (
	wmSetIcon = 0x0080
	iconSmall = 0
	iconBig   = 1

	imageIcon = 1
	lrShared  = 0x00008000

	smCxIcon   = 11 // SM_CXICON
	smCyIcon   = 12 // SM_CYICON
	smCxSmIcon = 49 // SM_CXSMICON
	smCySmIcon = 50 // SM_CYSMICON
)

func SetWindowIconClean(hwnd uintptr, resName string) bool {
	if hwnd == 0 || resName == "" {
		return false
	}

	namePtr, err := windows.UTF16PtrFromString(resName)
	if err != nil {
		return false
	}

	hInst, err := windows.GetModuleHandle(nil)
	if err != nil || hInst == 0 {
		return false
	}

	loadIcon := func(cxMetric, cyMetric uintptr) uintptr {
		cx, _, _ := procGetSystemMetrics.Call(cxMetric)
		cy, _, _ := procGetSystemMetrics.Call(cyMetric)
		hIcon, _, _ := procLoadImageW.Call(
			uintptr(hInst),
			uintptr(unsafe.Pointer(namePtr)),
			imageIcon,
			cx, cy,
			lrShared,
		)
		return hIcon
	}

	hIconSmall := loadIcon(smCxSmIcon, smCySmIcon)
	hIconBig := loadIcon(smCxIcon, smCyIcon)

	if hIconSmall != 0 {
		procSendMessageW.Call(hwnd, wmSetIcon, iconSmall, hIconSmall)
	}
	if hIconBig != 0 {
		procSendMessageW.Call(hwnd, wmSetIcon, iconBig, hIconBig)
	}

	return hIconSmall != 0 || hIconBig != 0
}
