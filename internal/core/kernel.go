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
	cm         *fsm.Manager
	hJob       windows.Handle
	currentPid uint32
	activeProc *os.Process
	mu         sync.Mutex
	lastError  string
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
	// ### 移除了 LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE (阻止 Windows 操作系统在句柄关闭时强制 Kill 子进程)
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
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

func (km *KernelManager) RunDaemon(ctx context.Context, eventCh chan<- KernelEvent) {
	target := filepath.Join(km.cm.BaseDir(), "mihomo.exe")
	absBaseDir, _ := filepath.Abs(km.cm.BaseDir())
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

		if km.cm.State.IsExiting() {
			return
		}

		// ### 删除了 sys.KillOtherProcessesByName("mihomo.exe", 0) 强杀残留进程的代码

		select {
		case <-ctx.Done():
			return
		case <-time.After(300 * time.Millisecond):
		}

		errBuf := &tailBuffer{max: 64 * 1024}

		// ### 改用 exec.Command 代替 exec.CommandContext，防止 Go 标准库在 ctx 结束时自动调用 proc.Kill() 强杀
		cmd := exec.Command(target, "-d", ".")
		cmd.Dir = absBaseDir

		const CREATE_DEFAULT_ERROR_MODE = 0x04000000
		
		cmd.SysProcAttr = &windows.SysProcAttr{
			HideWindow: true,
			CreationFlags: windows.CREATE_NO_WINDOW |
				windows.CREATE_NEW_PROCESS_GROUP |
				CREATE_DEFAULT_ERROR_MODE,
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

		km.assignToJob(cmd.Process.Pid)

		select {
		case eventCh <- EventKernelReady:
		default:
		}

		// ### 监听 Context 取消事件，收到取消通知时安全触发优雅退出
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
		isAppExiting := ctx.Err() != nil || km.cm.State.IsExiting() || isShutdown
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
			currentDelay = 50 * time.Millisecond 
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

func (km *KernelManager) assignToJob(pid int) {
	if km.hJob == 0 {
		return
	}
	if hp, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid)); err == nil {
		_ = windows.AssignProcessToJobObject(km.hJob, hp)
		windows.CloseHandle(hp)
	}
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

// ### 新增 helper：在 Windows 下安全将控制台信号送达后台无窗口子进程
func (km *KernelManager) sendCtrlBreak(pid uint32) error {
	if pid == 0 {
		return fmt.Errorf("invalid pid")
	}
	// 1. 动态附加到子进程的控制台上
	if err := windows.AttachConsole(pid); err != nil {
		return err
	}
	defer windows.FreeConsole()

	// 2. 暂时忽略主程序自身接收到的 Ctrl 信号，防止主程序跟着被误杀
	_ = windows.SetConsoleCtrlHandler(nil, true)
	defer _ = windows.SetConsoleCtrlHandler(nil, false)

	// 3. 向附加的控制台组广播 CTRL_BREAK_EVENT 软中断
	return windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, 0)
}

// KillCurrent 现已完全改造为“优雅安全关机”，不包含任何强杀行为
func (km *KernelManager) KillCurrent() {
	km.mu.Lock()
	proc := km.activeProc
	pid := atomic.LoadUint32(&km.currentPid)
	
	km.activeProc = nil
	atomic.StoreUint32(&km.currentPid, 0)
	km.mu.Unlock()

	if proc != nil && pid != 0 {
		// ### 使用 AttachConsole 方式向 mihomo 发送安全退出信号
		err := km.sendCtrlBreak(pid)
		
		if err == nil {
			// 最多等待 10 秒让内核安全释放资源（如清理 TUN 网卡）并自主退出
			for i := 0; i < 100; i++ {
				if !sys.IsPidRunning(pid, "mihomo.exe") {
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
			// ### 已完全删除超时后的 proc.Kill()
		} else {
			// ### 已完全删除信号发送失败后的 proc.Kill()
		}
	}

	// ### 已完全删除 sys.KillOtherProcessesByName("mihomo.exe", 0) 强杀代码
	time.Sleep(250 * time.Millisecond)
}
