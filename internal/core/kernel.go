package core

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

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
}

func NewKernelManager(cfg *config.Manager, st *state.RuntimeState) *KernelManager {
	km := &KernelManager{
		cfg: cfg,
		st:  st,
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

	for {
		select {
		case <-ctx.Done():
			km.KillCurrent()
			return
		default:
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

		sys.KillOtherProcessesByName("mihomo.exe", 0)

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
			currentDelay = km.calculateBackoff(currentDelay, maxDelay)
			select {
			case <-ctx.Done():
				return
			case <-time.After(currentDelay):
				continue
			}
		}

		km.mu.Lock()
		km.activeProc = cmd.Process
		atomic.StoreUint32(&km.currentPid, uint32(cmd.Process.Pid))
		km.mu.Unlock()

		sys.AssignProcessToJob(km.hJob, cmd.Process.Pid)

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

		if waitErr != nil && !isKilledByUs && !isAppExiting {
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

		if runDuration >= 5*time.Second || isKilledByUs {
			currentDelay = 600 * time.Millisecond
		} else {
			currentDelay = km.calculateBackoff(currentDelay, maxDelay)
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(currentDelay):
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

	log.Printf("[INFO] 正在向内核进程 (PID: %d) 发送安全退出中断 (CTRL_BREAK)...", pid)

	if err := sys.SendCtrlBreak(pid); err != nil {
		log.Printf("[ERROR] 发送安全中断失败: %v，尝试强制结束进程", err)
		_ = proc.Kill()
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
			log.Printf("[INFO] 内核进程 (PID: %d) 已完成清理并安全退出。", pid)
		} else {
			log.Printf("[WARN] 内核进程 (PID: %d) 退出超时，执行强制 kill 兜底...", pid)
			_ = proc.Kill()
		}
	}

	sys.KillOtherProcessesByName("mihomo.exe", 0)
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

	logPath := filepath.Join(absBaseDir, "error.log")
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
