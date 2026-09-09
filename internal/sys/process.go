package sys

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modKernel32Proc             = windows.NewLazySystemDLL("kernel32.dll")
	modUser32Process            = windows.NewLazySystemDLL("user32.dll")
	procAttachConsole           = modKernel32Proc.NewProc("AttachConsole")
	procFreeConsole             = modKernel32Proc.NewProc("FreeConsole")
	procSetConsoleCtrlHandler   = modKernel32Proc.NewProc("SetConsoleCtrlHandler")
	procGetSystemMetricsProcess = modUser32Process.NewProc("GetSystemMetrics")
)

func IsSystemShuttingDown() bool {
	const SM_SHUTTINGDOWN = 0x2000
	r, _, _ := procGetSystemMetricsProcess.Call(SM_SHUTTINGDOWN)
	return r != 0
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

	for {
		exeName := windows.UTF16ToString(pe.ExeFile[:])
		if strings.EqualFold(exeName, name) && pe.ProcessID != excludePid && pe.ProcessID != currentPid {
			slog.Debug("发现同名残留进程，准备结束", "目标", name, "PID", pe.ProcessID)
			h, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, pe.ProcessID)
			if err == nil {
				_ = windows.TerminateProcess(h, 9)
				_, _ = windows.WaitForSingleObject(h, 2000)
				windows.CloseHandle(h)
				slog.Debug("残留进程已结束并释放系统资源", "PID", pe.ProcessID)
			} else {
				slog.Error("结束残留进程失败 (拒绝访问)", "PID", pe.ProcessID, "err", err)
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

func CreateKillOnCloseJob() (windows.Handle, error) {
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
		slog.Error("配置进程组退出策略失败", "err", err)
	}
	return h, err
}

func AssignProcessToJob(hJob windows.Handle, pid int) {
	if hJob == 0 {
		return
	}
	if hp, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid)); err == nil {
		if err := windows.AssignProcessToJobObject(hJob, hp); err != nil {
			slog.Error("进程绑定 Job Object 失败", "pid", pid, "err", err)
		}
		windows.CloseHandle(hp)
	} else {
		slog.Error("获取进程句柄失败", "pid", pid, "err", err)
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
