//go:build windows

package sys

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procAttachConsole         = modKernel32.NewProc("AttachConsole")
	procFreeConsole           = modKernel32.NewProc("FreeConsole")
	procSetConsoleCtrlHandler = modKernel32.NewProc("SetConsoleCtrlHandler")
)

const (
	processTerminateWaitTimeout = 2 * time.Second
)

type Job struct {
	mu     sync.Mutex
	handle windows.Handle
}

func NewKillOnCloseJob() (*Job, error) {
	h, err := createKillOnCloseJob()
	if err != nil {
		return nil, err
	}
	return &Job{handle: h}, nil
}

func (j *Job) Assign(pid int) {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.handle != 0 {
		assignProcessToJob(j.handle, pid)
	}
}

func (j *Job) Terminate(exitCode uint32) error {
	if j == nil {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.handle != 0 {
		return windows.TerminateJobObject(j.handle, exitCode)
	}
	return nil
}

func (j *Job) Close() error {
	if j == nil {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.handle != 0 {
		err := windows.CloseHandle(j.handle)
		j.handle = 0
		return err
	}
	return nil
}

func createKillOnCloseJob() (windows.Handle, error) {
	h, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, fmt.Errorf("create job object: %w", err)
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	_, err = windows.SetInformationJobObject(
		h,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	)
	if err != nil {
		_ = windows.CloseHandle(h)
		return 0, fmt.Errorf("set job object limit: %w", err)
	}
	return h, nil
}

func assignProcessToJob(hJob windows.Handle, pid int) {
	if hJob == 0 || pid <= 0 {
		return
	}
	hp, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		slog.Warn("获取进程句柄失败", "pid", pid, "err", err)
		return
	}
	defer windows.CloseHandle(hp)

	if err := windows.AssignProcessToJobObject(hJob, hp); err != nil {
		slog.Warn("进程绑定 Job Object 失败", "pid", pid, "err", err)
	}
}

func matchProcessImagePath(h windows.Handle, buf []uint16, cleanAbsTarget string) bool {
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &size); err != nil {
		return false
	}
	fullPath := filepath.Clean(windows.UTF16ToString(buf[:size]))
	return strings.EqualFold(fullPath, cleanAbsTarget)
}

func KillOtherProcessesByPath(exePath string, excludePid uint32) {
	exePath = strings.TrimSpace(exePath)
	if exePath == "" {
		return
	}

	absTarget, err := filepath.Abs(exePath)
	if err != nil {
		return
	}
	absTarget = filepath.Clean(absTarget)
	targetName := filepath.Base(absTarget)

	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil || snapshot == windows.InvalidHandle {
		slog.Warn("枚举进程失败", "err", err)
		return
	}
	defer windows.CloseHandle(snapshot)

	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	if err := windows.Process32First(snapshot, &pe); err != nil {
		return
	}

	currentPid := windows.GetCurrentProcessId()
	waitMs := uint32(processTerminateWaitTimeout.Milliseconds())
	pathBuf := make([]uint16, 1024)

	for {
		procName := windows.UTF16ToString(pe.ExeFile[:])
		pid := pe.ProcessID
		if strings.EqualFold(procName, targetName) && pid != excludePid && pid != currentPid {
			if hQuery, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid); err == nil {
				if matchProcessImagePath(hQuery, pathBuf, absTarget) {
					if hTerm, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, pid); err == nil {
						_ = windows.TerminateProcess(hTerm, 9)
						_, _ = windows.WaitForSingleObject(hTerm, waitMs)
						_ = windows.CloseHandle(hTerm)
					} else {
						slog.Warn("清理残留进程失败", "pid", pid, "err", err)
					}
				}
				_ = windows.CloseHandle(hQuery)
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

	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, pid)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)

	event, err := windows.WaitForSingleObject(h, 0)
	if err != nil || event != uint32(windows.WAIT_TIMEOUT) {
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

func HardKill(pid uint32) {
	if pid == 0 {
		return
	}
	if h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, pid); err == nil {
		_ = windows.TerminateProcess(h, 0)
		_ = windows.CloseHandle(h)
	} else if IsPidRunning(pid, "") {
		slog.Warn("强制结束进程失败", "pid", pid, "err", err)
	}
}

func BackgroundProcAttr() *windows.SysProcAttr {
	const CREATE_DEFAULT_ERROR_MODE = 0x04000000
	return &windows.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | CREATE_DEFAULT_ERROR_MODE,
	}
}

func IsSystemShuttingDown() bool {
	const SM_SHUTTINGDOWN = 0x2000
	r, _, _ := procGetSystemMetrics.Call(SM_SHUTTINGDOWN)
	return r != 0
}
