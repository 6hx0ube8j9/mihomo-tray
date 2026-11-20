//go:build windows

package sys

import (
	"fmt"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modOle32   = windows.NewLazySystemDLL("ole32.dll")
	modShell32 = windows.NewLazySystemDLL("shell32.dll")

	procCoInitializeEx            = modOle32.NewProc("CoInitializeEx")
	procCoUninitialize            = modOle32.NewProc("CoUninitialize")
	procCoCreateInstance          = modOle32.NewProc("CoCreateInstance")
	procCoTaskMemFree             = modOle32.NewProc("CoTaskMemFree")
	procSHCreateItemFromParsingNm = modShell32.NewProc("SHCreateItemFromParsingName")
)

const (
	coinitApartmentThreaded = 0x2
	coinitDisableOle1DDE    = 0x4
	clsctxInprocServer      = 0x1

	sOK            = 0x00000000
	sFalse         = 0x00000001
	rpcEChangedMod = 0x80010106
	hrCancelled    = 0x800704C7

	fosOverwritePrompt = 0x00000002
	fosNoChangeDir     = 0x00000008
	fosForceFileSystem = 0x00000040
	fosPathMustExist   = 0x00000800
	fosFileMustExist   = 0x00001000
	fosDontAddToRecent = 0x02000000

	sigdnFileSysPath = 0x80058000
)

var (
	clsidFileOpenDialog = windows.GUID{
		Data1: 0xDC1C5A9C,
		Data2: 0xE88A,
		Data3: 0x4DDE,
		Data4: [8]byte{0xA5, 0xA1, 0x60, 0xF8, 0x2A, 0x20, 0xAE, 0xF7},
	}
	iidIFileOpenDialog = windows.GUID{
		Data1: 0xD57C7288,
		Data2: 0xD4AD,
		Data3: 0x4768,
		Data4: [8]byte{0xBE, 0x02, 0x9D, 0x96, 0x95, 0x32, 0xD9, 0x60},
	}
	iidIShellItem = windows.GUID{
		Data1: 0x43826D1E,
		Data2: 0xE718,
		Data3: 0x42EE,
		Data4: [8]byte{0xBC, 0x55, 0xA1, 0xE2, 0x61, 0xC3, 0x7B, 0xFE},
	}
)

type FileFilter struct {
	Name string
	Spec string
}

type FileDialogOptions struct {
	OwnerHWND  uintptr
	Title      string
	InitialDir string
	Filters    []FileFilter
}

type comdlgFilterSpec struct {
	pszName *uint16
	pszSpec *uint16
}

type comObject struct {
	vtbl *uintptr
}

func (o *comObject) method(index uintptr) uintptr {
	return *(*uintptr)(unsafe.Add(unsafe.Pointer(o.vtbl), index*unsafe.Sizeof(uintptr(0))))
}

func (o *comObject) release() {
	if o != nil {
		_, _, _ = syscall.SyscallN(o.method(2), uintptr(unsafe.Pointer(o)))
	}
}

func OpenFileDialog(opts FileDialogOptions) (string, bool, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	hrInit, _, _ := procCoInitializeEx.Call(0, coinitApartmentThreaded|coinitDisableOle1DDE)
	switch uint32(hrInit) {
	case sOK, sFalse:
		defer procCoUninitialize.Call()
	case rpcEChangedMod:
	default:
		return "", false, fmt.Errorf("CoInitializeEx failed: 0x%08x", uint32(hrInit))
	}

	var dlg *comObject
	hr, _, _ := procCoCreateInstance.Call(
		uintptr(unsafe.Pointer(&clsidFileOpenDialog)),
		0,
		clsctxInprocServer,
		uintptr(unsafe.Pointer(&iidIFileOpenDialog)),
		uintptr(unsafe.Pointer(&dlg)),
	)
	if int32(hr) < 0 || dlg == nil {
		return "", false, fmt.Errorf("CoCreateInstance(IFileOpenDialog) failed: 0x%08x", uint32(hr))
	}
	defer dlg.release()

	var currentOpts uint32
	_, _, _ = syscall.SyscallN(dlg.method(10), uintptr(unsafe.Pointer(dlg)), uintptr(unsafe.Pointer(&currentOpts)))
	newOpts := currentOpts | fosNoChangeDir | fosForceFileSystem | fosFileMustExist | fosPathMustExist | fosDontAddToRecent
	newOpts &^= fosOverwritePrompt
	_, _, _ = syscall.SyscallN(dlg.method(9), uintptr(unsafe.Pointer(dlg)), uintptr(newOpts))

	if opts.Title != "" {
		if titlePtr, err := windows.UTF16PtrFromString(opts.Title); err == nil {
			_, _, _ = syscall.SyscallN(dlg.method(17), uintptr(unsafe.Pointer(dlg)), uintptr(unsafe.Pointer(titlePtr)))
		}
	}

	if len(opts.Filters) > 0 {
		specs := make([]comdlgFilterSpec, 0, len(opts.Filters))
		backing := make([][]uint16, 0, len(opts.Filters)*2)
		for _, f := range opts.Filters {
			nSlice, err1 := windows.UTF16FromString(f.Name)
			sSlice, err2 := windows.UTF16FromString(f.Spec)
			if err1 == nil && err2 == nil {
				backing = append(backing, nSlice, sSlice)
				specs = append(specs, comdlgFilterSpec{
					pszName: &nSlice[0],
					pszSpec: &sSlice[0],
				})
			}
		}
		if len(specs) > 0 {
			_, _, _ = syscall.SyscallN(
				dlg.method(4),
				uintptr(unsafe.Pointer(dlg)),
				uintptr(len(specs)),
				uintptr(unsafe.Pointer(&specs[0])),
			)
			runtime.KeepAlive(backing)
			runtime.KeepAlive(specs)
		}
	}

	if opts.InitialDir != "" {
		if dirPtr, err := windows.UTF16PtrFromString(opts.InitialDir); err == nil {
			var folderItem *comObject
			hrDir, _, _ := procSHCreateItemFromParsingNm.Call(
				uintptr(unsafe.Pointer(dirPtr)),
				0,
				uintptr(unsafe.Pointer(&iidIShellItem)),
				uintptr(unsafe.Pointer(&folderItem)),
			)
			if int32(hrDir) >= 0 && folderItem != nil {
				_, _, _ = syscall.SyscallN(dlg.method(12), uintptr(unsafe.Pointer(dlg)), uintptr(unsafe.Pointer(folderItem)))
				folderItem.release()
			}
		}
	}

	hrShow, _, _ := syscall.SyscallN(dlg.method(3), uintptr(unsafe.Pointer(dlg)), opts.OwnerHWND)
	if uint32(hrShow) == hrCancelled {
		return "", false, nil
	}
	if int32(hrShow) < 0 {
		return "", false, fmt.Errorf("IFileOpenDialog.Show failed: 0x%08x", uint32(hrShow))
	}

	var resultItem *comObject
	hrRes, _, _ := syscall.SyscallN(dlg.method(20), uintptr(unsafe.Pointer(dlg)), uintptr(unsafe.Pointer(&resultItem)))
	if int32(hrRes) < 0 || resultItem == nil {
		return "", false, fmt.Errorf("IFileOpenDialog.GetResult failed: 0x%08x", uint32(hrRes))
	}
	defer resultItem.release()

	var pszPath *uint16
	hrName, _, _ := syscall.SyscallN(
		resultItem.method(5),
		uintptr(unsafe.Pointer(resultItem)),
		sigdnFileSysPath,
		uintptr(unsafe.Pointer(&pszPath)),
	)
	if int32(hrName) < 0 || pszPath == nil {
		return "", false, fmt.Errorf("IShellItem.GetDisplayName failed: 0x%08x", uint32(hrName))
	}
	defer procCoTaskMemFree.Call(uintptr(unsafe.Pointer(pszPath)))

	selected := windows.UTF16PtrToString(pszPath)
	return selected, selected != "", nil
}
