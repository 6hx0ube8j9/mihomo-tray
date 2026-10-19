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
	hMutex windows.Handle
	hEvent windows.Handle
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

func TryAcquireSingleInstance(mutexName, eventName string, oldPID uint32) (guard *SingleInstanceGuard, isOwner bool) {
	sa := getPermissiveSecAttr()
	mName, _ := windows.UTF16PtrFromString(mutexName)

	if oldPID > 1 {
		waitForOldProcessExit(oldPID, restartingProcessWaitTimeout)
	} else if oldPID == 1 {
		time.Sleep(200 * time.Millisecond)
	}

	hMutex, err := windows.CreateMutex(sa, false, mName)
	isAlreadyExist := errors.Is(err, windows.ERROR_ALREADY_EXISTS) ||
		errors.Is(err, windows.ERROR_ACCESS_DENIED) ||
		err == windows.ERROR_ALREADY_EXISTS ||
		err == windows.ERROR_ACCESS_DENIED

	if isAlreadyExist {
		if hMutex != 0 {
			_ = windows.CloseHandle(hMutex)
		}
		return nil, false
	}

	eName, _ := windows.UTF16PtrFromString(eventName)
	hEvent, _ := windows.CreateEvent(sa, 0, 0, eName)

	return &SingleInstanceGuard{
		hMutex: hMutex,
		hEvent: hEvent,
	}, true
}

func waitForOldProcessExit(pid uint32, timeout time.Duration) {
	hProcess, err := windows.OpenProcess(windows.SYNCHRONIZE, false, pid)
	if err != nil {
		return
	}
	defer windows.CloseHandle(hProcess)

	millis := uint32(timeout.Milliseconds())
	slog.Debug("等待旧进程释放资源", "pid", pid)
	_, _ = windows.WaitForSingleObject(hProcess, millis)
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
	if g == nil || g.hEvent == 0 {
		return
	}
	go func() {
		slog.Debug("监听进程唤醒事件")
		for {
			s, _ := windows.WaitForSingleObject(g.hEvent, windows.INFINITE)
			if s != windows.WAIT_OBJECT_0 || ctx.Err() != nil {
				return
			}
			slog.Info("捕获唤醒信号")
			onWake()
		}
	}()
}

func (g *SingleInstanceGuard) Close() {
	if g == nil {
		return
	}
	if g.hEvent != 0 {
		_ = windows.SetEvent(g.hEvent)
		_ = windows.CloseHandle(g.hEvent)
		g.hEvent = 0
	}
	if g.hMutex != 0 {
		_ = windows.CloseHandle(g.hMutex)
		g.hMutex = 0
	}
}
