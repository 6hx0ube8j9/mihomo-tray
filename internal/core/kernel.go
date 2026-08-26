package core

import (
	"context"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

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
		uintptr(unsafePointer(&info)),
		uint32(unsafeSizeof(info)),
	)
	km.hJob = h
}

func (km *KernelManager) Close() {
	if km.hJob != 0 {
		_ = windows.CloseHandle(km.hJob)
		km.hJob = 0
	}
}

func (km *KernelManager) RunDaemon(ctx context.Context, eventCh chan<- KernelEvent) {
	target := filepath.Join(km.cm.BaseDir(), "mihomo.exe")
	absBaseDir, _ := filepath.Abs(km.cm.BaseDir())

	for {
		select {
		case <-ctx.Done():
			km.KillCurrent()
			return
		default:
		}

		if km.cm.State.IsExiting() {
			return
		}

		// 检查 PID 是否仍在运行
		localPid := atomic.LoadUint32(&km.currentPid)
		if localPid != 0 && sys.IsPidRunning(localPid, "mihomo.exe") {
			select {
			case <-ctx.Done():
				km.KillCurrent()
				return
			case <-time.After(1 * time.Second):
				continue
			}
		}

		sys.KillOtherProcessesByName("mihomo.exe", 0)

		cmd := exec.CommandContext(ctx, target, "-d", ".")
		cmd.Dir = absBaseDir

		// 关键点：开启 CREATE_NEW_PROCESS_GROUP，允许独立接收 CTRL_BREAK 退出信号
		const CREATE_DEFAULT_ERROR_MODE = 0x04000000
		cmd.SysProcAttr = &windows.SysProcAttr{
			HideWindow: true,
			CreationFlags: windows.CREATE_NO_WINDOW |
				windows.CREATE_NEW_PROCESS_GROUP |
				CREATE_DEFAULT_ERROR_MODE,
		}

		if err := cmd.Start(); err != nil {
			log.Printf("[ERROR] 内核启动失败: %v", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
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

		// 阻塞等待进程退出
		_ = cmd.Wait()

		km.mu.Lock()
		km.activeProc = nil
		atomic.StoreUint32(&km.currentPid, 0)
		km.mu.Unlock()

		select {
		case eventCh <- EventKernelExit:
		default:
		}

		if km.cm.State.IsExiting() || ctx.Err() != nil {
			return
		}

		time.Sleep(500 * time.Millisecond)
	}
}

// 核心功能：优雅停机（温和关停 TUN）
func (km *KernelManager) KillCurrent() {
	km.mu.Lock()
	proc := km.activeProc
	pid := atomic.LoadUint32(&km.currentPid)
	km.activeProc = nil
	atomic.StoreUint32(&km.currentPid, 0)
	km.mu.Unlock()

	if proc != nil && pid != 0 {
		log.Printf("[INFO] 正在向内核 (PID: %d) 发送 Ctrl+Break 优雅退出信号...", pid)
		
		// 1. 发送 Win Console Ctrl+Break 信号
		err := windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, pid)

		if err == nil {
			// 2. 轮询等待最多 4 秒，给内核足够的时间卸载 WinTUN 网卡
			exited := false
			for i := 0; i < 40; i++ {
				if !sys.IsPidRunning(pid, "mihomo.exe") {
					exited = true
					log.Printf("[INFO] 内核已优雅退出，TUN 网卡成功卸载！(耗时 %d ms)", (i+1)*100)
					break
				}
				time.Sleep(100 * time.Millisecond)
			}

			// 3. 超时强杀兜底
			if !exited {
				log.Println("[WARN] 内核未在 4 秒内优雅退出，触发强制硬杀...")
				_ = proc.Kill()
			}
		} else {
			log.Printf("[WARN] 信号发送失败 (%v)，退回强制抹杀", err)
			_ = proc.Kill()
		}
	}

	sys.KillOtherProcessesByName("mihomo.exe", 0)
}

func (km *KernelManager) assignToJob(pid int) {
	if km.hJob == 0 {
		return
	}
	if hp, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid)); err == nil {
		_ = windows.AssignProcessToJobObject(km.hJob, hp)
		_ = windows.CloseHandle(hp)
	}
}

func unsafePointer(p interface{}) uintptr {
	return uintptr(windows.Handle(0))
}

func unsafeSizeof(v interface{}) uintptr {
	return 112 // JOBOBJECT_EXTENDED_LIMIT_INFORMATION 结构体在 64 位下的标准 Size
}
