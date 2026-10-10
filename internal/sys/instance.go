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

type SingleInstanceGuard struct {
	hMutex windows.Handle
	hEvent windows.Handle
}

func GrantForegroundPrivilege() {
	procAllowSetForegroundWnd.Call(ASFW_ANY)
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

func TryAcquireSingleInstance(mutexName, eventName string, isRestarting bool) (guard *SingleInstanceGuard, isOwner bool) {
	sa := getPermissiveSecAttr()
	mName, _ := windows.UTF16PtrFromString(mutexName)

	maxRetries := 1
	if isRestarting {
		maxRetries = 50
	}

	var hMutex windows.Handle
	var isAlreadyExist bool

	for i := 0; i < maxRetries; i++ {
		var err error
		hMutex, err = windows.CreateMutex(sa, false, mName)
		isAlreadyExist = errors.Is(err, windows.ERROR_ALREADY_EXISTS) ||
			errors.Is(err, windows.ERROR_ACCESS_DENIED) ||
			err == windows.ERROR_ALREADY_EXISTS ||
			err == windows.ERROR_ACCESS_DENIED

		if !isAlreadyExist {
			break
		}
		if hMutex != 0 {
			_ = windows.CloseHandle(hMutex)
			hMutex = 0
		}
		time.Sleep(200 * time.Millisecond)
	}

	if isAlreadyExist {
		return nil, false
	}

	eName, _ := windows.UTF16PtrFromString(eventName)
	hEvent, _ := windows.CreateEvent(sa, 0, 0, eName)

	return &SingleInstanceGuard{
		hMutex: hMutex,
		hEvent: hEvent,
	}, true
}

func NotifyExistingInstance(eventName string) {
	eName, _ := windows.UTF16PtrFromString(eventName)
	hEvent, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, eName)
	if err == nil && hEvent != 0 {
		GrantForegroundPrivilege()
		_ = windows.SetEvent(hEvent)
		_ = windows.CloseHandle(hEvent)
		time.Sleep(50 * time.Millisecond)
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
