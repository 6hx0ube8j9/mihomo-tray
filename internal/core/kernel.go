package core

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"log/slog"

	"golang.org/x/sys/windows"

	"mihomo-tray/internal/config"
	"mihomo-tray/internal/state"
	"mihomo-tray/internal/sys"
)

type KernelEvent int

const (
	EventKernelReady KernelEvent = iota
	EventKernelExit
)

type KernelManager struct {
	cfg        *config.Manager
	st         *state.RuntimeState
	hJob       windows.Handle
	currentPid uint32
	activeProc *os.Process
	mu         sync.Mutex
	killMu     sync.Mutex
	lastError  string
	isPaused   bool
	wakeCh     chan struct{}
}

func NewKernelManager(cfg *config.Manager, st *state.RuntimeState) *KernelManager {
	km := &KernelManager{
		cfg:    cfg,
		st:     st,
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

func (km *KernelManager) RunDaemon(ctx context.Context, eventCh chan<- KernelEvent) {
	target := filepath.Join(km.cfg.BaseDir(), "mihomo.exe")
	absBaseDir, _ := filepath.Abs(km.cfg.BaseDir())
	currentDelay := 50 * time.Millisecond
	const maxDelay = 30 * time.Second
	
	crashCount := 0
	
	quickCrashCount := 0         
	var firstCrashTime time.Time

	sys.KillOtherProcessesByName("mihomo.exe", 0)

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
		if localPid != 0 && sys.IsPidRunning(localPid, "mihomo.exe") {
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

		errBuf := &tailBuffer{max: 64 * 1024}

		cmd := exec.Command(target, "-d", ".")
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
			errMsg := fmt.Sprintf("启动错误: %v", err)
			km.checkAndWriteLog(absBaseDir, "ERROR", errMsg)

			if firstCrashTime.IsZero() {
				firstCrashTime = time.Now()
			}
			quickCrashCount++

			if quickCrashCount >= 15 {
				slog.Error("启动失败达到绝对上限，守护进程已熄火挂起，请排查故障后手动启动", "上限次数", 15)
				km.HaltDaemon()
				continue
			}

			if time.Since(firstCrashTime) >= 10*time.Minute {
				slog.Error("持续启动失败超过 10 分钟，守护进程已熄火挂起", "宽限时长", "10分钟")
				km.HaltDaemon()
				continue
			}

			crashCount++
			if crashCount >= 3 {
				slog.Error("连续启动失败达到上限，进入冷却", "failures", crashCount, "cooldown", "15s")
				currentDelay = 15 * time.Second
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
		
		slog.Info("启动内核进程", "PID", cmd.Process.Pid)

		km.mu.Lock()
		km.activeProc = cmd.Process
		atomic.StoreUint32(&km.currentPid, uint32(cmd.Process.Pid))
		km.mu.Unlock()

		sys.AssignProcessToJob(km.hJob, cmd.Process.Pid)
		slog.Debug("子进程绑定 Job Object 成功", "PID", cmd.Process.Pid)

		select {
		case <-ctx.Done():
			return
		case eventCh <- EventKernelReady:
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
		wasRunning := km.st.GetPhase() == state.PhaseRunning

		if isCrash {
			shouldLog := runDuration < 5*time.Second
			if !shouldLog {
				upperOut := strings.ToUpper(errBuf.String())
				shouldLog = strings.Contains(upperOut, "FATA") || strings.Contains(upperOut, "PANIC")
			}

			if shouldLog {
				rawErr := strings.TrimSpace(errBuf.String())
				errMsg := fmt.Sprintf("内核崩溃 | %v | %s", waitErr, rawErr)
				km.checkAndWriteLog(absBaseDir, "ERROR", errMsg)
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
		case eventCh <- EventKernelExit:
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

			if runDuration < 5*time.Second {
				quickCrashCount++
			}

			if quickCrashCount >= 15 {
				slog.Error("内核频繁秒退达到绝对上限，守护进程已熄火挂起，请排查配置后手动启动", "上限次数", 15)
				km.HaltDaemon()
				continue
			}

			if time.Since(firstCrashTime) >= 10*time.Minute {
				slog.Error("内核持续异常无法就绪已超过 10 分钟，守护进程已熄火挂起，请排查网络或配置", "宽限时长", "10分钟")
				km.HaltDaemon()
				continue
			}

			crashCount++
			if crashCount >= 3 {
				slog.Error("内核频繁异常退出，进入冷却", "failures", crashCount, "cooldown", "15s")
				currentDelay = 15 * time.Second
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

	slog.Info("发送进程退出信号", "PID", pid)

	if err := sys.SendCtrlBreak(pid); err != nil {
		slog.Error("发送退出信号失败，强制终止进程", "PID", pid, "err", err)
		_ = proc.Kill()
		sys.HardKill(pid)
		sys.KillOtherProcessesByName("mihomo.exe", 0) 
	} else {
		exited := false
		for i := 0; i < 100; i++ {
			if !sys.IsPidRunning(pid, "mihomo.exe") {
				exited = true
				break
			}
			time.Sleep(100 * time.Millisecond)
		}

		if exited {
			slog.Info("内核进程安全退出", "PID", pid)
		} else {
			slog.Warn("等待退出超时，强制终止进程", "PID", pid)
			_ = proc.Kill()
			sys.HardKill(pid)
			sys.KillOtherProcessesByName("mihomo.exe", 0)
		}
	}

	time.Sleep(250 * time.Millisecond)
}

func (km *KernelManager) checkAndWriteLog(absBaseDir, errType, rawMsg string) {
	cleanedMsg := rawMsg
	if idx := strings.Index(rawMsg, "level="); idx != -1 {
		cleanedMsg = rawMsg[idx:]
	}

	km.mu.Lock()
	if km.lastError == cleanedMsg {
		km.mu.Unlock()
		return
	}
	km.lastError = cleanedMsg
	km.mu.Unlock()

	logDir := filepath.Join(absBaseDir, "logs")
	_ = os.MkdirAll(logDir, 0755)

	logPath := filepath.Join(logDir, "core.log")
	
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	finalLog := fmt.Sprintf("[%s] [%s] %s\n----------------------------------------\n", timestamp, errType, rawMsg)

	fi, err := os.Stat(logPath)
	if err == nil && fi.Size()+int64(len(finalLog)) > 25*1024 {
		var keepData []byte
		f, err := os.Open(logPath)
		if err == nil {
			offset := fi.Size() - 5*1024
			if offset < 0 {
				offset = 0
			}
			keepData = make([]byte, fi.Size()-offset)
			_, _ = f.ReadAt(keepData, offset)
			f.Close()

			if offset > 0 {
				if idx := bytes.IndexByte(keepData, '\n'); idx != -1 {
					keepData = keepData[idx+1:]
				}
			}
		}

		notice := fmt.Sprintf("[%s] --- 日志大小已超限，仅保留最新部分 ---\n...\n", timestamp)
		combined := append(append([]byte(notice), keepData...), []byte(finalLog)...)
		_ = os.WriteFile(logPath, combined, 0644)
		return
	}

	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(finalLog)
}

func (km *KernelManager) calculateBackoff(current, max time.Duration) time.Duration {
	next := current * 2
	if next > max {
		return max
	}
	return next
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
		newBuf := make([]byte, t.max)
		copy(newBuf, t.buf[len(t.buf)-t.max:])
		t.buf = newBuf
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
	
	slog.Debug("写入唤醒信号")

	select {
	case km.wakeCh <- struct{}{}:
	default:
	}
}
