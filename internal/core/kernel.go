package core

import (
	"context"
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
	"mihomo-tray/internal/logger"
	"mihomo-tray/internal/state"
	"mihomo-tray/internal/sys"
)

const KernelReadyTimeout = 30 * time.Minute

const (
	MaxQuickCrashes     = 10
	MaxCrashWindow      = KernelReadyTimeout
	CoolDownCrashCount  = 3
	CoolDownDuration    = 15 * time.Second
	QuickCrashThreshold = 5 * time.Second
)

type TailBuffer struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func NewTailBuffer(maxSize int) *TailBuffer {
	return &TailBuffer{
		buf: make([]byte, 0, maxSize),
		max: maxSize,
	}
}

func (t *TailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		overflow := len(t.buf) - t.max
		copy(t.buf, t.buf[overflow:])
		t.buf = t.buf[:t.max]
	}
	return len(p), nil
}

func (t *TailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

func GetKernelPath(baseDir string) string {
	return filepath.Join(baseDir, domain.KernelExeName)
}

type KernelManager struct {
	cfg          *config.Manager
	st           *state.RuntimeState
	logger       *logger.CoreLogger
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
		logger: logger.NewCoreLogger(cfg.BaseDir()),
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
	if km.logger != nil {
		_ = km.logger.Close()
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
			km.logger.WriteLog(domain.LogTagKernelTransition, err.Error())

			if firstCrashTime.IsZero() {
				firstCrashTime = time.Now()
			}
			quickCrashCount++

			if quickCrashCount >= MaxQuickCrashes || time.Since(firstCrashTime) >= MaxCrashWindow {
				slog.Error("内核启动失败次数超限，守护进程已挂起保护")
				km.HaltDaemon()
				continue
			}

			crashCount++
			if crashCount >= CoolDownCrashCount {
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

		slog.Debug("内核底层进程已运行", "PID", cmd.Process.Pid)

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
			rawErr := strings.TrimSpace(errBuf.String())
			upperOut := strings.ToUpper(rawErr)
			isConfigFatal := strings.Contains(upperOut, "FATA") || strings.Contains(upperOut, "PANIC")
			shouldLog := runDuration < QuickCrashThreshold || isConfigFatal

			if shouldLog {
				logContent := rawErr
				if logContent == "" {
					logContent = waitErr.Error()
				}
				km.logger.WriteLog(domain.LogTagKernelTransition, logContent)
			}

			if isConfigFatal {
				slog.Error("配置存在致命错误，内核守护进程已挂起")
				km.HaltDaemon()

				select {
				case eventCh <- domain.EventKernelExit:
				default:
				}
				continue
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

			if quickCrashCount >= MaxQuickCrashes || time.Since(firstCrashTime) >= MaxCrashWindow {
				slog.Error("内核异常退出超限，守护进程已挂起保护")
				km.HaltDaemon()
				continue
			}

			crashCount++
			if crashCount >= CoolDownCrashCount {
				slog.Warn("内核退出频繁，进入冷却等待", "cooldown", CoolDownDuration)
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

	forceKill := func() {
		_ = proc.Kill()
		sys.HardKill(pid)
		sys.KillOtherProcessesByName(domain.KernelExeName, 0)
	}

	if err := sys.SendCtrlBreak(pid); err != nil {
		forceKill()
	} else {
		exited := false
		for i := 0; i < 30; i++ {
			if !sys.IsPidRunning(pid, domain.KernelExeName) {
				exited = true
				break
			}
			time.Sleep(50 * time.Millisecond)
		}

		if !exited {
			forceKill()
		}
	}
	time.Sleep(100 * time.Millisecond)
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

func (km *KernelManager) IsRunning() bool {
	km.mu.Lock()
	defer km.mu.Unlock()
	return km.activeProc != nil && atomic.LoadUint32(&km.currentPid) != 0
}

func (km *KernelManager) WriteCoreLog(errType, rawMsg string) {
	if km.logger != nil {
		km.logger.WriteLog(errType, rawMsg)
	}
}
