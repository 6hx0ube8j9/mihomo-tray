// instance.go manages application single-instance enforcement, cross-integrity IPC
// wake-up signaling, and foreground privilege delegation.
// Primarily invoked by main.go during process bootstrapping and secondary instance dispatch.

package sys

import (
	"context"
	"errors"
	"log/slog"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procAllowSetForegroundWnd = modUser32.NewProc("AllowSetForegroundWindow")
)

const (
	asfwAny                      = 0xFFFFFFFF
	restartingProcessWaitTimeout = 10 * time.Second
)

type SingleInstanceGuard struct {
	hMutex  windows.Handle
	hEvent  windows.Handle
	hCancel windows.Handle
}

func GrantForegroundPrivilege() {
	procAllowSetForegroundWnd.Call(asfwAny)
}

func getPermissiveSecAttr() *windows.SecurityAttributes {
	sd, err := windows.SecurityDescriptorFromString("D:(A;;GA;;;WD)S:(ML;;NW;;;LW)")
	if err != nil {
		return nil
	}
	var sa windows.SecurityAttributes
	sa.Length = uint32(unsafe.Sizeof(sa))
	sa.SecurityDescriptor = sd
	return &sa
}

func TryAcquireSingleInstance(mutexName, eventName string, oldPID uint32) (*SingleInstanceGuard, bool) {
	sa := getPermissiveSecAttr()

	if oldPID > 1 {
		waitForOldProcessExit(oldPID, restartingProcessWaitTimeout)
	} else if oldPID == 1 {
		time.Sleep(200 * time.Millisecond)
	}

	mName, _ := windows.UTF16PtrFromString(mutexName)
	hMutex, err := windows.CreateMutex(sa, false, mName)
	
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) || errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		if hMutex != 0 {
			_ = windows.CloseHandle(hMutex)
		}
		return nil, false
	}
	if err != nil || hMutex == 0 {
		slog.Error("创建全局单实例互斥体失败", "error", err)
		return nil, false
	}

	eName, _ := windows.UTF16PtrFromString(eventName)
	hEvent, _ := windows.CreateEvent(sa, 0, 0, eName)
	hCancel, _ := windows.CreateEvent(nil, 0, 0, nil)

	return &SingleInstanceGuard{
		hMutex:  hMutex,
		hEvent:  hEvent,
		hCancel: hCancel,
	}, true
}

func waitForOldProcessExit(pid uint32, timeout time.Duration) {
	hProcess, err := windows.OpenProcess(windows.SYNCHRONIZE, false, pid)
	if err != nil {
		return
	}
	defer windows.CloseHandle(hProcess)

	slog.Debug("等待旧进程释放系统资源", "pid", pid)
	_, _ = windows.WaitForSingleObject(hProcess, uint32(timeout.Milliseconds()))
}

func NotifyExistingInstance(eventName string) {
	eName, _ := windows.UTF16PtrFromString(eventName)
	hEvent, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, eName)
	if err == nil && hEvent != 0 {
		GrantForegroundPrivilege()
		_ = windows.SetEvent(hEvent)
		_ = windows.CloseHandle(hEvent)
	}
}

func (g *SingleInstanceGuard) ListenWakeEvent(ctx context.Context, onWake func()) {
	if g == nil || g.hEvent == 0 || g.hCancel == 0 {
		return
	}

	stopCancel := context.AfterFunc(ctx, func() {
		_ = windows.SetEvent(g.hCancel)
	})

	go func() {
		defer stopCancel()
		handles := [2]windows.Handle{g.hEvent, g.hCancel}

		for {
			event, err := windows.WaitForMultipleObjects(handles[:], false, windows.INFINITE)
			if err != nil || event != windows.WAIT_OBJECT_0 {
				return
			}

			if ctx.Err() == nil {
				slog.Info("收到外部唤醒信号")
				onWake()
			}
		}
	}()
}

func (g *SingleInstanceGuard) Close() {
	if g == nil {
		return
	}
	if g.hCancel != 0 {
		_ = windows.SetEvent(g.hCancel)
		_ = windows.CloseHandle(g.hCancel)
		g.hCancel = 0
	}
	if g.hEvent != 0 {
		_ = windows.CloseHandle(g.hEvent)
		g.hEvent = 0
	}
	if g.hMutex != 0 {
		_ = windows.CloseHandle(g.hMutex)
		g.hMutex = 0
	}
}
