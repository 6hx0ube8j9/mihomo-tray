package core

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"mihomo-tray/internal/fsm"
	"mihomo-tray/internal/sys"
)

type KernelEvent int

const (
	EventKernelReady KernelEvent = iota
	EventKernelExit
)

type KernelManager struct {
	cm                *fsm.Manager
	hJob              windows.Handle
	currentPid        uint32
	activeProc        *os.Process
	consecutiveErrors int
	mu                sync.Mutex
}

func NewKernelManager(cm *fsm.Manager) *KernelManager {
	km := &KernelManager{cm: cm}
	km.initJobObject()
	return km
}

func (km *KernelManager) initJobObject() {
	h, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return
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
	km.hJob = h
}

func (km *KernelManager) Close() {
	if km.hJob != 0 {
		windows.CloseHandle(km.hJob)
		km.hJob = 0
	}
}

func (km *KernelManager) ResetCrashCounter() {
	km.mu.Lock()
	defer km.mu.Unlock()
	km.consecutiveErrors = 0
}

func (km *KernelManager) RunDaemon(ctx context.Context, eventCh chan<- KernelEvent) {
	var baseDir string
	if exePath, err := os.Executable(); err == nil {
		baseDir = filepath.Dir(exePath)
	} else {
		baseDir = km.cm.BaseDir()
	}
	absBaseDir, _ := filepath.Abs(baseDir)
	target := filepath.Join(absBaseDir, "mihomo.exe")

	const crashThreshold = 5 * time.Second

	for {
		select {
		case <-ctx.Done():
			km.KillCurrent()
			return
		default:
		}

		if km.cm.State.GetPhase() == fsm.PhaseKernelPanic {
			time.Sleep(1 * time.Second)
			continue
		}

		localPid := atomic.LoadUint32(&km.currentPid)
		if localPid != 0 && sys.IsPidRunning(localPid, "mihomo.exe") {
			time.Sleep(2 * time.Second)
			continue
		}

		if km.cm.State.IsExiting() {
			return
		}

		exists := false
		for i := 0; i < 5; i++ {
			if _, err := os.Stat(target); err == nil {
				exists = true
				break
			}
			if km.cm.State.IsExiting() || ctx.Err() != nil {
				return
			}
			time.Sleep(3 * time.Second)
		}

		if !exists {
			km.cm.State.SetPhase(fsm.PhaseKernelPanic)
			km.cm.State.SetKernelError("未找到内核可执行文件！\n\n请检查程序同目录下是否存在 mihomo.exe，它可能已被安全软件误杀或隔离。")
			continue
		}

		sys.KillOtherProcessesByName("mihomo.exe", 0)
		time.Sleep(300 * time.Millisecond)
		errBuf := &tailBuffer{max: 64 * 1024}

		cmd := exec.CommandContext(ctx, target, "-d", ".")
		cmd.Dir = absBaseDir
		cmd.SysProcAttr = &windows.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
		cmd.Stdout = errBuf
		cmd.Stderr = errBuf

		startTime := time.Now()

		if err := cmd.Start(); err != nil {
			km.consecutiveErrors++
			if km.consecutiveErrors >= 3 {
				km.cm.State.SetPhase(fsm.PhaseKernelPanic)
				km.consecutiveErrors = 0
				km.cm.State.SetKernelError(fmt.Sprintf("内核创建进程失败！\n\n系统返回错误: %v\n\n请确认该内核文件是否与您的 CPU 架构兼容，或者检查杀毒软件拦截记录。", err))
			}
			time.Sleep(2 * time.Second)
			continue
		}

		km.mu.Lock()
		km.activeProc = cmd.Process
		atomic.StoreUint32(&km.currentPid, uint32(cmd.Process.Pid))
		km.mu.Unlock()

		if km.hJob != 0 {
			if hp, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid)); err == nil {
				_ = windows.AssignProcessToJobObject(km.hJob, hp)
				windows.CloseHandle(hp)
			}
		}

		select {
		case eventCh <- EventKernelReady:
		default:
		}
		
		waitErr := cmd.Wait()

		km.mu.Lock()
		isKilledByUs := (km.activeProc == nil)
		km.mu.Unlock()

		isAppExiting := false
		if ctx.Err() != nil || km.cm.State.IsExiting() {
			isAppExiting = true
		}

		finalOutput := errBuf.String()

		if !isKilledByUs && !isAppExiting {
			runDuration := time.Since(startTime)

			if runDuration < crashThreshold {
				km.consecutiveErrors++
			} else {
				km.consecutiveErrors = 0 
			}

			upperOut := strings.ToUpper(finalOutput)
			if waitErr != nil || strings.Contains(upperOut, "FATA") || strings.Contains(upperOut, "PANIC") {
				logPath := filepath.Join(absBaseDir, "error.log")
				finalLog := fmt.Sprintf("Kernel Exit Status: %v\nKernel Output:\n%s", waitErr, finalOutput)
				_ = os.WriteFile(logPath, []byte(finalLog), 0644)
			}

			// 如果闪退次数超限，触发熔断
			if km.consecutiveErrors >= 3 {
				km.cm.State.SetPhase(fsm.PhaseKernelPanic)
				km.consecutiveErrors = 0 // 重置计数器，等待下一次手动触发
				km.cm.State.SetKernelError("内核连续启动失败，已启动熔断保护。\n\n这通常是由于：\n1. 端口（如 9090, 7890）被其他程序占用。\n2. 内核版本与您的 CPU 架构不兼容。\n3. config.yaml 配置文件格式错误。\n\n请点击【编辑配置】或【打开程序目录】查看 error.log，排查后在【更多】中点击【重启核心】重试。")
			}
		} else {
			if runDuration := time.Since(startTime); runDuration >= crashThreshold {
				km.consecutiveErrors = 0
			}
		}

		km.mu.Lock()
		km.activeProc = nil
		atomic.StoreUint32(&km.currentPid, 0)
		km.mu.Unlock()

		select {
		case eventCh <- EventKernelExit:
		default:
		}

		time.Sleep(1 * time.Second)
	}
}

type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

func (t *tailBuffer) Len() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.buf)
}

func (km *KernelManager) KillCurrent() {
	km.mu.Lock()
	if km.activeProc != nil {
		_ = km.activeProc.Kill()
		km.activeProc = nil
	}
	atomic.StoreUint32(&km.currentPid, 0)
	km.mu.Unlock()
	sys.KillOtherProcessesByName("mihomo.exe", 0)
	time.Sleep(300 * time.Millisecond)
}
