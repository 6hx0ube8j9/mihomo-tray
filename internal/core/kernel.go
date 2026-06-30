package core

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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

	for {
		select {
		case <-ctx.Done():
			km.KillCurrent()
			return
		default:
		}

		localPid := atomic.LoadUint32(&km.currentPid)
		if localPid != 0 && sys.IsPidRunning(localPid, "mihomo.exe") {    
			time.Sleep(2 * time.Second)
			continue
		}

		if km.cm.State.IsExiting() {
			return
		}

		sys.KillOtherProcessesByName("mihomo.exe", 0)
		time.Sleep(300 * time.Millisecond)

		var errBuf bytes.Buffer

		cmd := exec.CommandContext(ctx, target, "-d", ".")
		cmd.Dir = absBaseDir
		cmd.SysProcAttr = &windows.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
		cmd.Stdout = io.Discard
		cmd.Stderr = &errBuf

		if err := cmd.Start(); err != nil {
			time.Sleep(2 * time.Second)
			continue
		}

		km.mu.Lock()
		km.activeProc = cmd.Process
		atomic.StoreUint32(&km.currentPid, uint32(cmd.Process.Pid))
		km.mu.Unlock()

		if km.hJob != 0 {
			if hp, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid)); err == nil {
				_ = windows.AssignProcessToJobObject(km.hJob, hp)
				windows.CloseHandle(hp)
			}
		}

		select {
		case eventCh <- EventKernelReady:
		default:
		}

		_ = cmd.Wait()

		if errBuf.Len() > 0 {
			logPath := filepath.Join(km.cm.BaseDir(), "error.log")
			_ = os.WriteFile(logPath, errBuf.Bytes(), 0644)
		}

		km.mu.Lock()
		km.activeProc = nil
		atomic.StoreUint32(&km.currentPid, 0)
		km.mu.Unlock()

		select {
		case eventCh <- EventKernelExit:
		default:
		}

		time.Sleep(1 * time.Second)
	}
}

func (km *KernelManager) KillCurrent() {
	km.mu.Lock()
	if km.activeProc != nil {
		_ = km.activeProc.Kill()
		km.activeProc = nil
	}
	atomic.StoreUint32(&km.currentPid, 0)
	km.mu.Unlock()
	sys.KillOtherProcessesByName("mihomo.exe", 0)
	time.Sleep(300 * time.Millisecond)
}
