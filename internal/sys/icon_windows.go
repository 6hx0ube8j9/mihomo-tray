package sys

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procGetModuleHandleW = modKernel32.NewProc("GetModuleHandleW")
	procLoadImageW       = modUser32.NewProc("LoadImageW")
	procSendMessageW     = modUser32.NewProc("SendMessageW")
)

const (
	wmSetIcon = 0x0080
	iconSmall = 0
	iconBig   = 1

	imageIcon = 1
	lrShared  = 0x00008000

	smCxIcon   = 11
	smCyIcon   = 12
	smCxSmIcon = 49
	smCySmIcon = 50
)

func SetWindowIcon(hwnd uintptr, resName string) bool {
	if hwnd == 0 || resName == "" {
		return false
	}

	namePtr, err := windows.UTF16PtrFromString(resName)
	if err != nil {
		return false
	}

	hInst, _, _ := procGetModuleHandleW.Call(0)
	if hInst == 0 {
		return false
	}

	cxSm, _, _ := procGetSystemMetrics.Call(smCxSmIcon)
	cySm, _, _ := procGetSystemMetrics.Call(smCySmIcon)
	hIconSmall, _, _ := procLoadImageW.Call(
		hInst,
		uintptr(unsafe.Pointer(namePtr)),
		imageIcon,
		cxSm, cySm,
		lrShared,
	)

	cxLg, _, _ := procGetSystemMetrics.Call(smCxIcon)
	cyLg, _, _ := procGetSystemMetrics.Call(smCyIcon)
	hIconBig, _, _ := procLoadImageW.Call(
		hInst,
		uintptr(unsafe.Pointer(namePtr)),
		imageIcon,
		cxLg, cyLg,
		lrShared,
	)

	if hIconSmall != 0 {
		procSendMessageW.Call(hwnd, wmSetIcon, iconSmall, hIconSmall)
	}
	if hIconBig != 0 {
		procSendMessageW.Call(hwnd, wmSetIcon, iconBig, hIconBig)
	}

	return hIconSmall != 0 || hIconBig != 0
}
