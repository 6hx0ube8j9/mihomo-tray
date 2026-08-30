package sys

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

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

func CreateKillOnCloseJob() (windows.Handle, error) {
	h, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	_, _ = windows.SetInformationJobObject(
		h,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	)
	return h, nil
}

func AssignProcessToJob(hJob windows.Handle, pid int) {
	if hJob == 0 {
		return
	}
	if hp, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid)); err == nil {
		_ = windows.AssignProcessToJobObject(hJob, hp)
		windows.CloseHandle(hp)
	}
}

func SendCtrlBreak(pid uint32) error {
	if pid == 0 {
		return fmt.Errorf("invalid pid")
	}

	procSetConsoleCtrlHandler.Call(0, 1)
	defer procSetConsoleCtrlHandler.Call(0, 0)
	procFreeConsole.Call()

	r1, _, err := procAttachConsole.Call(uintptr(pid))
	if r1 == 0 {
		return fmt.Errorf("attachConsole 失败: %w", err)
	}
	
	defer procFreeConsole.Call()
	return windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, pid)
}
