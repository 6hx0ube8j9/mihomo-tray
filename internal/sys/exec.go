package sys

import (
	"log"
	"unsafe"
	"golang.org/x/sys/windows"
)

var (
	modUser32Exec   = windows.NewLazySystemDLL("user32.dll")
	procMessageBoxW = modUser32Exec.NewProc("MessageBoxW")
)

func ExecuteSystemCommand(path string) error {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	return windows.ShellExecute(0, nil, pathPtr, nil, nil, windows.SW_SHOWNORMAL)
}

func RunAsAdmin(exe, dir string) {
	verb, _ := windows.UTF16PtrFromString("runas")
	exePtr, _ := windows.UTF16PtrFromString(exe)
	cwdPtr, _ := windows.UTF16PtrFromString(dir)
	err := windows.ShellExecute(0, verb, exePtr, nil, cwdPtr, windows.SW_SHOWNORMAL)
	
	if err != nil {
		log.Printf("[ERROR] 触发 UAC 提权失败: %v", err)
		// MB_ICONERROR | MB_TOPMOST = 0x10 | 0x40000 = 0x40010
		title, _ := windows.UTF16PtrFromString("权限请求失败")
		msg, _ := windows.UTF16PtrFromString("请以管理员权限重新运行程序。")
		procMessageBoxW.Call(0, uintptr(unsafe.Pointer(msg)), uintptr(unsafe.Pointer(title)), 0x40010)
	}
}
