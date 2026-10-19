package sys

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
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
	if j != nil && j.handle != 0 {
		assignProcessToJob(j.handle, pid)
	}
}

func (j *Job) Close() error {
	if j != nil && j.handle != 0 {
		err := windows.CloseHandle(j.handle)
		j.handle = 0
		return err
	}
	return nil
}

func createKillOnCloseJob() (windows.Handle, error) {
	h, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		slog.Error("创建 Job Object 失败", "err", err)
		return 0, err
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
		slog.Error("配置 Job Object 退出策略失败", "err", err)
	}
	return h, err
}

func assignProcessToJob(hJob windows.Handle, pid int) {
	if hJob == 0 || pid <= 0 {
		return
	}
	hp, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		slog.Error("获取进程句柄失败", "pid", pid, "err", err)
		return
	}
	defer windows.CloseHandle(hp)

	if err := windows.AssignProcessToJobObject(hJob, hp); err != nil {
		slog.Error("进程绑定 Job Object 失败", "pid", pid, "err", err)
	}
}

func KillOtherProcessesByName(name string, excludePid uint32) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil || snapshot == windows.InvalidHandle {
		slog.Error("创建进程快照失败", "err", err)
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

	for {
		exeName := windows.UTF16ToString(pe.ExeFile[:])
		if strings.EqualFold(exeName, name) && pe.ProcessID != excludePid && pe.ProcessID != currentPid {
			slog.Debug("发现同名残留进程，正在终止", "name", name, "pid", pe.ProcessID)
			h, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, pe.ProcessID)
			if err == nil {
				_ = windows.TerminateProcess(h, 9)
				_, _ = windows.WaitForSingleObject(h, waitMs)
				windows.CloseHandle(h)
				slog.Debug("已终止同名残留进程", "pid", pe.ProcessID)
			} else {
				slog.Error("终止同名残留进程失败", "pid", pe.ProcessID, "err", err)
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
		slog.Error("附加目标控制台失败", "pid", pid, "err", err)
		return fmt.Errorf("attachConsole 失败: %w", err)
	}

	defer procFreeConsole.Call()
	return windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, pid)
}

func HardKill(pid uint32) {
	if pid == 0 {
		return
	}
	slog.Debug("强制终止进程", "pid", pid)
	if h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, pid); err == nil {
		_ = windows.TerminateProcess(h, 0)
		windows.CloseHandle(h)
	} else {
		slog.Error("强制终止进程失败", "pid", pid, "err", err)
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
