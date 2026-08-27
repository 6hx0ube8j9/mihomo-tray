package core

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
			HideWindow: true,
			CreationFlags: windows.CREATE_NEW_PROCESS_GROUP |
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

		childPid := uint32(cmd.Process.Pid)

		km.mu.Lock()
		km.activeProc = cmd.Process
		atomic.StoreUint32(&km.currentPid, childPid)
		km.mu.Unlock()

		km.assignToJob(cmd.Process.Pid)

		// 启动后台守护看门狗进程
		km.startWatchdog(childPid)

		select {
		case eventCh <- EventKernelReady:
		default:
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

// 启动看门狗子进程
func (km *KernelManager) startWatchdog(childPid uint32) {
	exePath, err := os.Executable()
	if err != nil {
		return
	}
	parentPid := uint32(os.Getpid())

	watchdogCmd := exec.Command(exePath, "--watchdog", fmt.Sprint(parentPid), fmt.Sprint(childPid))
	watchdogCmd.SysProcAttr = &windows.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NO_WINDOW,
	}
	_ = watchdogCmd.Start()
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

// 动态加载 kernel32.dll 底层 Win32 控制台 API
var (
	modkernel32               = windows.NewLazySystemDLL("kernel32.dll")
	procAttachConsole         = modkernel32.NewProc("AttachConsole")
	procFreeConsole           = modkernel32.NewProc("FreeConsole")
	procSetConsoleCtrlHandler = modkernel32.NewProc("SetConsoleCtrlHandler")
)

func attachConsole(pid uint32) error {
	r1, _, err := procAttachConsole.Call(uintptr(pid))
	if r1 == 0 {
		return err
	}
	return nil
}

func freeConsole() error {
	r1, _, err := procFreeConsole.Call()
	if r1 == 0 {
		return err
	}
	return nil
}

func setConsoleCtrlHandler(add bool) error {
	var a uintptr
	if add {
		a = 1
	}
	r1, _, err := procSetConsoleCtrlHandler.Call(0, a)
	if r1 == 0 {
		return err
	}
	return nil
}

// 安全控制台中断发送函数（静态全局可用）
func sendCtrlBreakStatic(pid uint32) error {
	if pid == 0 {
		return fmt.Errorf("invalid pid")
	}

	if err := attachConsole(pid); err != nil {
		return fmt.Errorf("attachConsole(pid=%d) 失败: %w", pid, err)
	}
	defer freeConsole()

	_ = setConsoleCtrlHandler(true)
	defer setConsoleCtrlHandler(false)

	return windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, pid)
}

func (km *KernelManager) sendCtrlBreak(pid uint32) error {
	return sendCtrlBreakStatic(pid)
}

// CheckWatchdogArg 检查命令行参数，如果是看门狗模式则执行看门狗逻辑
func CheckWatchdogArg() bool {
	if len(os.Args) >= 4 && os.Args[1] == "--watchdog" {
		parentPid, _ := strconv.ParseUint(os.Args[2], 10, 32)
		childPid, _ := strconv.ParseUint(os.Args[3], 10, 32)
		runWatchdog(uint32(parentPid), uint32(childPid))
		return true
	}
	return false
}

// 看门狗核心逻辑
func runWatchdog(parentPid, childPid uint32) {
	if parentPid == 0 || childPid == 0 {
		return
	}

	hParent, err := windows.OpenProcess(windows.SYNCHRONIZE, false, parentPid)
	if err != nil {
		return
	}
	defer windows.CloseHandle(hParent)

	hChild, err := windows.OpenProcess(windows.SYNCHRONIZE, false, childPid)
	if err != nil {
		return
	}
	defer windows.CloseHandle(hChild)

	handles := []windows.Handle{hParent, hChild}

	// 阻塞等待：主程序退出 或 内核退出（CPU占用 0%）
	event, err := windows.WaitForMultipleObjects(handles, false, windows.INFINITE)
	if err != nil {
		return
	}

	if event == windows.WAIT_OBJECT_0 {
		// 替主程序向内核发送 CTRL_BREAK 优雅退出信号
		_ = sendCtrlBreakStatic(childPid)
		// 等待内核 safe exit，最多等待 10 秒
		_, _ = windows.WaitForSingleObject(hChild, 10000)
	}
}

func (km *KernelManager) KillCurrent() {
	km.mu.Lock()
	proc := km.activeProc
	pid := atomic.LoadUint32(&km.currentPid)

	km.activeProc = nil
	atomic.StoreUint32(&km.currentPid, 0)
	km.mu.Unlock()

	if proc != nil && pid != 0 {
		log.Printf("[INFO] 正在向内核 (PID: %d) 发送安全退出信号 (CTRL_BREAK)...", pid)
		err := km.sendCtrlBreak(pid)

		if err == nil {
			for i := 0; i < 100; i++ {
				if !sys.IsPidRunning(pid, "mihomo.exe") {
					log.Printf("[INFO] 内核进程 (PID: %d) 已安全退出。", pid)
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
		} else {
			log.Printf("[ERROR] 发送安全退出信号失败: %v", err)
		}
	}

	time.Sleep(250 * time.Millisecond)
}
