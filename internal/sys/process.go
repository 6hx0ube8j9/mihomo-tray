package sys

import (
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modUser32            = windows.NewLazySystemDLL("user32.dll")
	procGetSystemMetrics = modUser32.NewProc("GetSystemMetrics")
)

func IsSystemShuttingDown() bool {
	const SM_SHUTTINGDOWN = 0x2000
	r, _, _ := procGetSystemMetrics.Call(SM_SHUTTINGDOWN)
	return r != 0
}

func KillOtherProcessesByName(name string, excludePid uint32) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil || snapshot == windows.InvalidHandle {
		return
	}
	defer windows.CloseHandle(snapshot)

	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	if err := windows.Process32First(snapshot, &pe); err != nil {
		return
	}

	currentPid := windows.GetCurrentProcessId()

	for {
		exeName := windows.UTF16ToString(pe.ExeFile[:])
		
		if strings.EqualFold(exeName, name) && pe.ProcessID != excludePid && pe.ProcessID != currentPid {
			h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, pe.ProcessID)
			if err == nil {
				_ = windows.TerminateProcess(h, 9)
				windows.CloseHandle(h)
				time.Sleep(50 * time.Millisecond)
			}
		}
		if err := windows.Process32Next(snapshot, &pe); err != nil {
			break
		}
	}
}

func IsPidRunning(pid uint32, expectedExeName string) bool {
	if pid == 0 {
		return false
	}
	
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)

	var exitCode uint32
	if err := windows.GetExitCodeProcess(h, &exitCode); err != nil {
		return false
	}
	if exitCode != 259 { 
		return false
	}

	if expectedExeName == "" {
		return true
	}

	buf := make([]uint16, windows.MAX_PATH)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &size); err == nil {
		fullPath := windows.UTF16ToString(buf[:size])
		baseName := filepath.Base(fullPath)
		return strings.EqualFold(baseName, expectedExeName)
	}

	return false
}

func ExecuteSystemCommand(path string) error {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	return windows.ShellExecute(0, nil, pathPtr, nil, nil, 1)
}
