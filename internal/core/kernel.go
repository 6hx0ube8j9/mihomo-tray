package core

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sys/windows"

	"mihomo-tray/internal/config"
	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/state"
	"mihomo-tray/internal/sys"
)

const (
	MaxQuickCrashes     = 15               // 绝对启动失败上限次数
	MaxCrashWindow      = 10 * time.Minute // 持续失败的宽限时间
	CoolDownCrashCount  = 3                // 触发冷却的连续失败次数
	CoolDownDuration    = 15 * time.Second // 冷却时长
	QuickCrashThreshold = 5 * time.Second  // 判定为秒退的时间阈值
)

func GetKernelPath(baseDir string) string {
	return filepath.Join(baseDir, domain.KernelExeName)
}

type KernelManager struct {
	cfg          *config.Manager
	st           *state.RuntimeState
	logger       *CoreLogger
	hJob         windows.Handle
	currentPid   uint32
	activeProc   *os.Process
	mu           sync.Mutex
	killMu       sync.Mutex
	isPaused     bool
	wakeCh       chan struct{}
	preStartHook func()
}

func (km *KernelManager) SetPreStartHook(hook func()) {
	km.mu.Lock()
	km.preStartHook = hook
	km.mu.Unlock()
}

func NewKernelManager(cfg *config.Manager, st *state.RuntimeState) *KernelManager {
	km := &KernelManager{
		cfg:    cfg,
		st:     st,
		logger: NewCoreLogger(cfg.BaseDir()),
		wakeCh: make(chan struct{}, 1),
	}
	km.hJob, _ = sys.CreateKillOnCloseJob()
	return km
}

func (km *KernelManager) Close() {
	if km.hJob != 0 {
		windows.CloseHandle(km.hJob)
		km.hJob = 0
	}
}

func (km *KernelManager) RunDaemon(ctx context.Context, eventCh chan<- domain.KernelEvent) {
	target := GetKernelPath(km.cfg.BaseDir())
	absBaseDir, _ := filepath.Abs(km.cfg.BaseDir())
	currentDelay := 50 * time.Millisecond
	const maxDelay = 30 * time.Second

	crashCount := 0
	quickCrashCount := 0
	var firstCrashTime time.Time

	sys.KillOtherProcessesByName(domain.KernelExeName, 0)

	for {
		select {
		case <-ctx.Done():
			km.KillCurrent()
			return
		default:
		}

		km.mu.Lock()
		paused := km.isPaused
		km.mu.Unlock()

		if paused {
			select {
			case <-km.wakeCh:
				quickCrashCount = 0
				firstCrashTime = time.Time{}
				crashCount = 0
				currentDelay = 50 * time.Millisecond
			case <-ctx.Done():
				return
			}
			continue
		}

		localPid := atomic.LoadUint32(&km.currentPid)
		if localPid != 0 && sys.IsPidRunning(localPid, domain.KernelExeName) {
			select {
			case <-ctx.Done():
				km.KillCurrent()
				return
			case <-time.After(2 * time.Second):
				continue
			}
		}

		if km.st.IsExiting() {
			return
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(300 * time.Millisecond):
		}

		km.mu.Lock()
		hook := km.preStartHook
		km.mu.Unlock()
		if hook != nil {
			hook()
		}

		errBuf := NewTailBuffer(64 * 1024)
		runtimeAbs := filepath.Join(absBaseDir, domain.RuntimeConfigName)

		cmd := exec.Command(target, "-d", ".", "-f", runtimeAbs)
		cmd.Dir = absBaseDir

		const CREATE_DEFAULT_ERROR_MODE = 0x04000000
		cmd.SysProcAttr = &windows.SysProcAttr{
			HideWindow:    true,
			CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | CREATE_DEFAULT_ERROR_MODE,
		}

		cmd.Stdout = errBuf
		cmd.Stderr = errBuf
		startTime := time.Now()

		if err := cmd.Start(); err != nil {
			errMsg := fmt.Sprintf("进程启动失败: %v", err)
			km.logger.WriteLog("ERROR", errMsg)

			if firstCrashTime.IsZero() {
				firstCrashTime = time.Now()
			}
			quickCrashCount++

			if quickCrashCount >= MaxQuickCrashes {
				slog.Error("启动失败次数达到上限，进程已挂起，请手动干预")
				km.HaltDaemon()
				continue
			}

			if time.Since(firstCrashTime) >= MaxCrashWindow {
				slog.Error("持续启动失败超时，进程已挂起")
				km.HaltDaemon()
				continue
			}

			crashCount++
			if crashCount >= CoolDownCrashCount {
				slog.Error("连续启动失败，进程进入冷却状态", "cooldown", CoolDownDuration)
				currentDelay = CoolDownDuration
				crashCount = 0
			} else {
				currentDelay = km.calculateBackoff(currentDelay, maxDelay)
			}

			select {
			case <-ctx.Done():
				return
			case <-time.After(currentDelay):
			case <-km.wakeCh:
				quickCrashCount = 0
				firstCrashTime = time.Time{}
				crashCount = 0
				currentDelay = 50 * time.Millisecond
			}
			continue
		}

		slog.Info("内核进程已启动", "PID", cmd.Process.Pid)

		km.mu.Lock()
		km.activeProc = cmd.Process
		atomic.StoreUint32(&km.currentPid, uint32(cmd.Process.Pid))
		km.mu.Unlock()

		sys.AssignProcessToJob(km.hJob, cmd.Process.Pid)

		select {
		case <-ctx.Done():
			return
		case eventCh <- domain.EventKernelReady:
		}

		waitDone := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				km.KillCurrent()
			case <-waitDone:
			}
		}()

		waitErr := cmd.Wait()
		close(waitDone)

		km.mu.Lock()
		isKilledByUs := (km.activeProc == nil)
		km.mu.Unlock()

		isShutdown := sys.IsSystemShuttingDown()
		isAppExiting := ctx.Err() != nil || km.st.IsExiting() || isShutdown
		runDuration := time.Since(startTime)

		isCrash := waitErr != nil && !isKilledByUs && !isAppExiting
		wasRunning := km.st.GetPhase() == domain.PhaseRunning

		if isCrash {
			shouldLog := runDuration < QuickCrashThreshold
			if !shouldLog {
				upperOut := strings.ToUpper(errBuf.String())
				shouldLog = strings.Contains(upperOut, "FATA") || strings.Contains(upperOut, "PANIC")
			}

			if shouldLog {
				rawErr := strings.TrimSpace(errBuf.String())
				errMsg := fmt.Sprintf("内核崩溃 | %v | %s", waitErr, rawErr)
				km.logger.WriteLog("ERROR", errMsg)
			}
		}

		if isShutdown {
			return
		}

		km.mu.Lock()
		km.activeProc = nil
		atomic.StoreUint32(&km.currentPid, 0)
		km.mu.Unlock()

		select {
		case eventCh <- domain.EventKernelExit:
		default:
		}

		if isCrash {
			if wasRunning {
				quickCrashCount = 0
				firstCrashTime = time.Time{}
			}

			if firstCrashTime.IsZero() {
				firstCrashTime = time.Now()
			}

			if runDuration < QuickCrashThreshold {
				quickCrashCount++
			}

			if quickCrashCount >= MaxQuickCrashes {
				slog.Error("内核频繁崩溃达到上限，进程已挂起，请检查配置")
				km.HaltDaemon()
				continue
			}

			if time.Since(firstCrashTime) >= MaxCrashWindow {
				slog.Error("内核持续异常超时，进程已挂起，请检查网络或配置")
				km.HaltDaemon()
				continue
			}

			crashCount++
			if crashCount >= CoolDownCrashCount {
				slog.Error("内核异常退出频繁，进入冷却状态", "cooldown", CoolDownDuration)
				currentDelay = CoolDownDuration
				crashCount = 0
			} else {
				currentDelay = km.calculateBackoff(currentDelay, maxDelay)
			}
		} else {
			quickCrashCount = 0
			firstCrashTime = time.Time{}
			crashCount = 0
			currentDelay = 600 * time.Millisecond
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(currentDelay):
		case <-km.wakeCh:
			quickCrashCount = 0
			firstCrashTime = time.Time{}
			crashCount = 0
			currentDelay = 50 * time.Millisecond
		}
	}
}

func (km *KernelManager) KillCurrent() {
	km.killMu.Lock()
	defer km.killMu.Unlock()

	km.mu.Lock()
	proc := km.activeProc
	pid := atomic.LoadUint32(&km.currentPid)

	if proc == nil || pid == 0 {
		km.mu.Unlock()
		return
	}

	km.activeProc = nil
	atomic.StoreUint32(&km.currentPid, 0)
	km.mu.Unlock()

	if err := sys.SendCtrlBreak(pid); err != nil {
		_ = proc.Kill()
		sys.HardKill(pid)
		sys.KillOtherProcessesByName(domain.KernelExeName, 0)
	} else {
		exited := false
		for i := 0; i < 100; i++ {
			if !sys.IsPidRunning(pid, domain.KernelExeName) {
				exited = true
				break
			}
			time.Sleep(100 * time.Millisecond)
		}

		if !exited {
			_ = proc.Kill()
			sys.HardKill(pid)
			sys.KillOtherProcessesByName(domain.KernelExeName, 0)
		}
	}
	time.Sleep(250 * time.Millisecond)
}

func (km *KernelManager) calculateBackoff(current, max time.Duration) time.Duration {
	next := current * 2
	if next > max {
		return max
	}
	return next
}

func (km *KernelManager) HaltDaemon() {
	km.mu.Lock()
	km.isPaused = true
	km.mu.Unlock()
	km.KillCurrent()
}

func (km *KernelManager) WakeDaemon() {
	km.mu.Lock()
	km.isPaused = false
	km.mu.Unlock()
	select {
	case km.wakeCh <- struct{}{}:
	default:
	}
}

func (km *KernelManager) IsPaused() bool {
	km.mu.Lock()
	defer km.mu.Unlock()
	return km.isPaused
}

func (km *KernelManager) WriteCoreLog(errType, rawMsg string) {
	if km.logger != nil {
		km.logger.WriteLog(errType, rawMsg)
	}
}
