package core

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/logger"
	"mihomo-tray/internal/sys"
)

const (
	maxCrashWindow      = 30 * time.Minute
	readyTimeout        = 30 * time.Minute
	readyPollInterval   = 200 * time.Millisecond
	slowPollInterval    = 2 * time.Second
	quickCrashThreshold = 5 * time.Second
	maxConsecutiveCrash = 5
	defaultRestartDelay = 500 * time.Millisecond
	maxRestartDelay     = 15 * time.Second
)

var ErrKernelExitedEarly = errors.New("内核进程已提前退出")

func GetKernelPath(baseDir string) string {
	return filepath.Join(baseDir, domain.KernelExeName)
}

type sessionResult struct {
	runDuration  time.Duration
	wasReady     bool
	isCrash      bool
	startErr     error
	crashLog     string
	isAppExiting bool
}

type crashBackoff struct {
	count      int
	firstCrash time.Time
	delay      time.Duration
}

func newCrashBackoff() crashBackoff {
	return crashBackoff{delay: defaultRestartDelay}
}

func (b *crashBackoff) reset() {
	b.count = 0
	b.firstCrash = time.Time{}
	b.delay = defaultRestartDelay
}

func (b *crashBackoff) recordCrash(runDuration time.Duration) bool {
	if runDuration >= quickCrashThreshold {
		return false
	}

	now := time.Now()
	if b.firstCrash.IsZero() || now.Sub(b.firstCrash) >= maxCrashWindow {
		b.firstCrash = now
		b.count = 1
	} else {
		b.count++
	}
	return b.count >= maxConsecutiveCrash
}

func (b *crashBackoff) advanceDelay() {
	next := b.delay * 2
	if next > maxRestartDelay {
		next = maxRestartDelay
	}
	b.delay = next
}

type KernelManager struct {
	baseDir     string
	isExitingFn func() bool
	api         *APIClient
	logger      *logger.CoreLogger
	runner      *ProcessRunner

	mu       sync.Mutex
	isPaused bool
	wakeCh   chan struct{}
}

func NewKernelManager(baseDir string, isExiting func() bool, api *APIClient) *KernelManager {
	return &KernelManager{
		baseDir:     baseDir,
		isExitingFn: isExiting,
		api:         api,
		logger:      logger.NewCoreLogger(baseDir),
		runner:      NewProcessRunner(GetKernelPath(baseDir)),
		wakeCh:      make(chan struct{}, 1),
	}
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
	backoff := newCrashBackoff()

	km.runner.CleanupOrphans()

	for {
		if ctx.Err() != nil || km.isExiting() {
			km.runner.Kill()
			return
		}

		if km.waitIfPaused(ctx) {
			backoff.reset()
			continue
		}

		res := km.runSession(ctx, eventCh)
		if res.isAppExiting {
			return
		}

		var exitEvent domain.KernelEvent
		halted := false
		if res.isCrash {
			halted = km.handleSessionCrash(res, &backoff)
			if halted {
				exitEvent = domain.EventKernelHalted
			} else {
				exitEvent = domain.EventKernelCrashed
			}
		} else {
			backoff.reset()
			exitEvent = domain.EventKernelStopped
		}

		select {
		case <-ctx.Done():
			return
		case eventCh <- exitEvent:
		}

		if halted {
			continue
		}

		km.waitBackoff(ctx, backoff.delay)
	}
}

func (km *KernelManager) runSession(ctx context.Context, eventCh chan<- domain.KernelEvent) sessionResult {
	absBaseDir, _ := filepath.Abs(km.baseDir)
	target := GetKernelPath(absBaseDir)
	runtimeAbs := filepath.Join(absBaseDir, domain.RuntimeConfigName)
	args := []string{"-d", ".", "-f", runtimeAbs}

	errBuf := logger.NewTailBuffer()
	startTime := time.Now()

	if err := km.runner.Start(absBaseDir, target, args, errBuf); err != nil {
		return sessionResult{
			runDuration:  0,
			isCrash:      true,
			startErr:     err,
			isAppExiting: ctx.Err() != nil || km.isExiting(),
		}
	}
	if km.api != nil {
		defer km.api.CloseIdleConnections()
	}

	readyCtx, cancelReady := context.WithTimeout(ctx, readyTimeout)
	readyErr := km.WaitForReady(readyCtx)
	cancelReady()

	switch readyErr {
	case nil:
		select {
		case <-ctx.Done():
			km.runner.Kill()
			return sessionResult{isAppExiting: true}
		case eventCh <- domain.EventKernelReady:
		}
	case context.DeadlineExceeded:
		if km.writeCrashLog(strings.TrimSpace(errBuf.String())) {
			slog.Error("内核就绪超时，详细信息请查看 core.log")
		} else {
			slog.Error("内核就绪超时")
		}
		km.runner.Kill()
	}

	waitErr := km.runner.Wait(ctx)
	runDuration := time.Since(startTime)

	isShutdown := sys.IsSystemShuttingDown()
	isAppExiting := ctx.Err() != nil || km.isExiting() || isShutdown
	isCrash := (waitErr != nil || readyErr != nil) && !km.runner.IsKilledByUs() && !isAppExiting

	return sessionResult{
		runDuration:  runDuration,
		wasReady:     readyErr == nil,
		isCrash:      isCrash,
		crashLog:     strings.TrimSpace(errBuf.String()),
		isAppExiting: isAppExiting,
	}
}

func (km *KernelManager) WaitForReady(ctx context.Context) error {
	if km.api == nil {
		return nil
	}

	ticker := time.NewTicker(readyPollInterval)
	defer ticker.Stop()

	startTime := time.Now()
	hasLoggedSlow := false

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if km.isExiting() {
				return context.Canceled
			}

			if !km.runner.IsRunning() {
				return ErrKernelExitedEarly
			}

			reqCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			err := km.api.Ping(reqCtx)
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

func (km *KernelManager) writeCrashLog(crashLog string) bool {
	if km.logger == nil || crashLog == "" {
		return false
	}
	written := false
	for _, line := range strings.Split(crashLog, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && km.logger.WriteLog(line) {
			written = true
		}
	}
	return written
}

func (km *KernelManager) handleSessionCrash(res sessionResult, backoff *crashBackoff) (halted bool) {
	if res.startErr != nil {
		isFirst := backoff.count == 0
		if backoff.recordCrash(res.runDuration) {
			slog.Error("内核连续启动失败，已暂停", "err", res.startErr)
			backoff.reset()
			km.HaltDaemon()
			return true
		}
		if isFirst {
			slog.Error("启动内核进程失败", "err", res.startErr)
		}
		backoff.advanceDelay()
		return false
	}

	upper := strings.ToUpper(res.crashLog)
	isFatal := strings.Contains(upper, "FATA") || strings.Contains(upper, "PANIC")
	hasCoreLog := km.writeCrashLog(res.crashLog)

	logCrash := func(msg string) {
		if hasCoreLog {
			msg += "，详细信息请查看 core.log"
		}
		slog.Error(msg)
	}

	if isFatal {
		logCrash("内核致命错误，已暂停")
		backoff.reset()
		km.HaltDaemon()
		return true
	}

	if res.wasReady && res.runDuration >= quickCrashThreshold {
		logCrash("内核异常退出，正在重启")
		backoff.reset()
		return false
	}

	if backoff.recordCrash(res.runDuration) {
		logCrash("内核连续异常退出，已暂停")
		backoff.reset()
		km.HaltDaemon()
		return true
	}

	backoff.advanceDelay()
	return false
}

func (km *KernelManager) isExiting() bool {
	return km.isExitingFn != nil && km.isExitingFn()
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
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
	case <-timer.C:
	case <-km.wakeCh:
	}
}

func (km *KernelManager) HaltDaemon() {
	km.mu.Lock()
	km.isPaused = true
	select {
	case <-km.wakeCh:
	default:
	}
	km.mu.Unlock()
	km.KillCurrent()
}

func (km *KernelManager) IsPaused() bool {
	km.mu.Lock()
	defer km.mu.Unlock()
	return km.isPaused
}

func (km *KernelManager) KillCurrent() {
	if km.runner != nil {
		km.runner.Kill()
	}
}

func (km *KernelManager) Restart() {
	km.mu.Lock()
	km.isPaused = false
	km.mu.Unlock()

	km.KillCurrent()
	select {
	case km.wakeCh <- struct{}{}:
	default:
	}
}
