package core

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"mihomo-tray/internal/config"
	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/logger"
	"mihomo-tray/internal/state"
	"mihomo-tray/internal/sys"
)

const (
	KernelReadyTimeout  = 30 * time.Minute
	MaxQuickCrashes     = 10
	MaxCrashWindow      = KernelReadyTimeout
	CoolDownCrashCount  = 3
	CoolDownDuration    = 15 * time.Second
	QuickCrashThreshold = 5 * time.Second
	readyProbeTimeout   = 10 * time.Second
	readyPollInterval   = 200 * time.Millisecond
	defaultRestartDelay = 600 * time.Millisecond
	minRestartDelay     = 50 * time.Millisecond
	maxRestartDelay     = 30 * time.Second
	crashLogBufferSize  = 64 * 1024
)

func GetKernelPath(baseDir string) string {
	return filepath.Join(baseDir, domain.KernelExeName)
}

type KernelManager struct {
	cfg    *config.Manager
	st     *state.RuntimeState
	logger *logger.CoreLogger
	client *APIClient
	runner *ProcessRunner

	mu           sync.Mutex
	isPaused     bool
	wakeCh       chan struct{}
	preStartHook func()
}

func NewKernelManager(cfg *config.Manager, st *state.RuntimeState, client *APIClient) *KernelManager {
	return &KernelManager{
		cfg:    cfg,
		st:     st,
		logger: logger.NewCoreLogger(cfg.BaseDir()),
		client: client,
		runner: NewProcessRunner(),
		wakeCh: make(chan struct{}, 1),
	}
}

func (km *KernelManager) SetPreStartHook(hook func()) {
	km.mu.Lock()
	defer km.mu.Unlock()
	km.preStartHook = hook
}

func (km *KernelManager) Close() {
	if km.runner != nil {
		km.runner.Close()
	}
	if km.logger != nil {
		_ = km.logger.Close()
	}
}

func (km *KernelManager) RunDaemon(ctx context.Context, eventCh chan<- domain.KernelEvent) {
	target := GetKernelPath(km.cfg.BaseDir())
	absBaseDir, _ := filepath.Abs(km.cfg.BaseDir())
	runtimeAbs := filepath.Join(absBaseDir, domain.RuntimeConfigName)

	currentDelay := minRestartDelay
	crashCount := 0
	quickCrashCount := 0
	var firstCrashTime time.Time

	km.runner.CleanupOrphans()

	for {
		if ctx.Err() != nil {
			km.runner.Kill()
			return
		}

		if km.waitIfPaused(ctx) {
			quickCrashCount = 0
			firstCrashTime = time.Time{}
			crashCount = 0
			currentDelay = minRestartDelay
			continue
		}

		if km.runner.IsRunning() {
			select {
			case <-ctx.Done():
				km.runner.Kill()
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

		km.runPreStartHook()

		errBuf := logger.NewTailBuffer(crashLogBufferSize)
		startTime := time.Now()

		if err := km.runner.Start(absBaseDir, target, runtimeAbs, errBuf); err != nil {
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
				currentDelay = km.calculateBackoff(currentDelay, maxRestartDelay)
			}

			km.waitBackoff(ctx, currentDelay)
			continue
		}

		slog.Debug("内核底层进程已运行", "PID", km.runner.Pid())

		readyCtx, readyCancel := context.WithTimeout(ctx, readyProbeTimeout)
		readyErr := km.waitForReady(readyCtx)
		readyCancel()

		if readyErr != nil {
			slog.Error("内核启动后未能按时就绪，强制回收", "err", readyErr)
			km.runner.Kill()
			km.waitBackoff(ctx, currentDelay)
			continue
		}

		select {
		case <-ctx.Done():
			return
		case eventCh <- domain.EventKernelReady:
		}

		waitErr := km.runner.Wait(ctx)
		runDuration := time.Since(startTime)

		isShutdown := sys.IsSystemShuttingDown()
		isAppExiting := ctx.Err() != nil || km.st.IsExiting() || isShutdown
		isCrash := waitErr != nil && !km.runner.IsKilledByUs() && !isAppExiting
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
				currentDelay = km.calculateBackoff(currentDelay, maxRestartDelay)
			}
		} else {
			quickCrashCount = 0
			firstCrashTime = time.Time{}
			crashCount = 0
			currentDelay = defaultRestartDelay
		}

		km.waitBackoff(ctx, currentDelay)
	}
}

func (km *KernelManager) WaitForReady(ctx context.Context) error {
	ticker := time.NewTicker(readyPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if km.st.IsExiting() {
				return context.Canceled
			}

			if !km.runner.IsRunning() {
				return errors.New("内核进程已提前退出")
			}

			reqCtx, cancel := context.WithTimeout(ctx, readyPollInterval)
			err := km.client.Ping(reqCtx)
			cancel()

			if err == nil {
				return nil
			}
		}
	}
}

func (km *KernelManager) waitIfPaused(ctx context.Context) bool {
	km.mu.Lock()
	paused := km.isPaused
	km.mu.Unlock()

	if !paused {
		return false
	}

	select {
	case <-km.wakeCh:
		return true
	case <-ctx.Done():
		return false
	}
}

func (km *KernelManager) waitBackoff(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	case <-km.wakeCh:
	}
}

func (km *KernelManager) runPreStartHook() {
	km.mu.Lock()
	hook := km.preStartHook
	km.mu.Unlock()
	if hook != nil {
		hook()
	}
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
	return km.runner != nil && km.runner.IsRunning()
}

func (km *KernelManager) KillCurrent() {
	if km.runner != nil {
		km.runner.Kill()
	}
}

func (km *KernelManager) WriteCoreLog(errType, rawMsg string) {
	if km.logger != nil {
		km.logger.WriteLog(errType, rawMsg)
	}
}
