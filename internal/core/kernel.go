package core

import (
	"context"
	"errors"
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
	readyPollInterval   = 200 * time.Millisecond
	slowPollInterval    = 2 * time.Second
	quickCrashThreshold = 5 * time.Second
	maxConsecutiveCrash = 5
	defaultRestartDelay = 500 * time.Millisecond
	maxRestartDelay     = 15 * time.Second
)

var ErrKernelExitedEarly = errors.New("内核进程已提前退出")

type sessionResult struct {
	runDuration  time.Duration
	isCrash      bool
	crashLog     string
	isAppExiting bool
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
	currentDelay := defaultRestartDelay
	consecutiveCrashes := 0

	km.runner.CleanupOrphans()

	for {
		if ctx.Err() != nil || km.st.IsExiting() {
			km.runner.Kill()
			return
		}

		if km.waitIfPaused(ctx) {
			consecutiveCrashes = 0
			currentDelay = defaultRestartDelay
			continue
		}

		res := km.runSession(ctx, eventCh)
		if res.isAppExiting {
			return
		}

		select {
		case eventCh <- domain.EventKernelExit:
		default:
		}

		if res.isCrash {
			currentDelay = km.handleSessionCrash(res, &consecutiveCrashes, currentDelay)
		} else {
			consecutiveCrashes = 0
			currentDelay = defaultRestartDelay
		}

		km.waitBackoff(ctx, currentDelay)
	}
}

func (km *KernelManager) runSession(ctx context.Context, eventCh chan<- domain.KernelEvent) sessionResult {
	km.runPreStartHook()

	target := filepath.Join(km.cfg.BaseDir(), domain.KernelExeName)
	absBaseDir, _ := filepath.Abs(km.cfg.BaseDir())
	runtimeAbs := filepath.Join(absBaseDir, domain.RuntimeConfigName)

	errBuf := logger.NewTailBuffer()
	startTime := time.Now()

	if err := km.runner.Start(absBaseDir, target, runtimeAbs, errBuf); err != nil {
		km.logger.WriteLog(domain.LogTagKernelTransition, err.Error())
		return sessionResult{
			runDuration:  0,
			isCrash:      true,
			crashLog:     err.Error(),
			isAppExiting: ctx.Err() != nil || km.st.IsExiting(),
		}
	}

	slog.Debug("内核进程已启动", "pid", km.runner.Pid())

	readyErr := km.WaitForReady(ctx)
	if readyErr == nil {
		select {
		case <-ctx.Done():
			km.runner.Kill()
			return sessionResult{isAppExiting: true}
		case eventCh <- domain.EventKernelReady:
		}
	}

	waitErr := km.runner.Wait(ctx)
	runDuration := time.Since(startTime)

	isShutdown := sys.IsSystemShuttingDown()
	isAppExiting := ctx.Err() != nil || km.st.IsExiting() || isShutdown
	isCrash := (waitErr != nil || readyErr != nil) && !km.runner.IsKilledByUs() && !isAppExiting

	return sessionResult{
		runDuration:  runDuration,
		isCrash:      isCrash,
		crashLog:     strings.TrimSpace(errBuf.String()),
		isAppExiting: isAppExiting,
	}
}

func (km *KernelManager) WaitForReady(ctx context.Context) error {
	ticker := time.NewTicker(readyPollInterval)
	defer ticker.Stop()

	startTime := time.Now()
	hasLoggedSlow := false

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if km.st.IsExiting() {
				return context.Canceled
			}

			if !km.runner.IsRunning() {
				return ErrKernelExitedEarly
			}

			reqCtx, cancel := context.WithTimeout(ctx, readyPollInterval)
			err := km.client.Ping(reqCtx)
			cancel()

			if err == nil {
				return nil
			}

			if time.Since(startTime) > 10*time.Second {
				if !hasLoggedSlow {
					ticker.Reset(slowPollInterval)
					slog.Info("等待内核启动中...")
					hasLoggedSlow = true
				}
			}
		}
	}
}

func (km *KernelManager) handleSessionCrash(res sessionResult, crashCount *int, currentDelay time.Duration) time.Duration {
	wasRunning := km.st.GetPhase() == domain.PhaseRunning
	upperLog := strings.ToUpper(res.crashLog)
	isConfigFatal := strings.Contains(upperLog, "FATA") || strings.Contains(upperLog, "PANIC")

	if res.runDuration < quickCrashThreshold || isConfigFatal {
		if res.crashLog != "" {
			km.logger.WriteLog(domain.LogTagKernelTransition, res.crashLog)
		}
	}

	if isConfigFatal {
		slog.Error("配置存在致命错误，已暂停启动")
		km.HaltDaemon()
		return defaultRestartDelay
	}

	if res.runDuration < quickCrashThreshold {
		if wasRunning {
			*crashCount = 0
		}
		*crashCount++

		if *crashCount >= maxConsecutiveCrash {
			slog.Error("内核连续异常退出，已暂停启动")
			km.HaltDaemon()
			return defaultRestartDelay
		}
		return km.calculateBackoff(currentDelay, maxRestartDelay)
	}

	*crashCount = 0
	return defaultRestartDelay
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
