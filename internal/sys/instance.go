package sys

import (
	"context"
	"errors"

	"golang.org/x/sys/windows"
)

var procAllowSetForegroundWnd = modUser32.NewProc("AllowSetForegroundWindow")

type SingleInstanceGuard struct {
	hMutex windows.Handle
	hEvent windows.Handle
}

func TryAcquireSingleInstance(mutexName, eventName string, _ uint32) (*SingleInstanceGuard, bool) {
	hMutex, err := windows.CreateMutex(nil, true, windows.StringToUTF16Ptr(mutexName))
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) || errors.Is(err, windows.ERROR_ACCESS_DENIED) || hMutex == 0 {
		if hMutex != 0 {
			_ = windows.CloseHandle(hMutex)
		}
		return nil, false
	}

	hEvent, _ := windows.CreateEvent(nil, 0, 0, windows.StringToUTF16Ptr(eventName))
	return &SingleInstanceGuard{hMutex: hMutex, hEvent: hEvent}, true
}

func NotifyExistingInstance(eventName string) {
	// 广播借调特权（0xFFFFFFFF 为通配借调）
	_, _, _ = procAllowSetForegroundWnd.Call(0xFFFFFFFF)

	if hEvent, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, windows.StringToUTF16Ptr(eventName)); err == nil && hEvent != 0 {
		_ = windows.SetEvent(hEvent)
		_ = windows.CloseHandle(hEvent)
	}
}

func (g *SingleInstanceGuard) ListenWakeEvent(ctx context.Context, onWake func()) {
	if g == nil || g.hEvent == 0 {
		return
	}
	go func() {
		for {
			res, err := windows.WaitForSingleObject(g.hEvent, windows.INFINITE)
			if err != nil || res != windows.WAIT_OBJECT_0 {
				return
			}
			if ctx.Err() != nil {
				return
			}
			onWake()
		}
	}()
}

func (g *SingleInstanceGuard) Close() {
	if g == nil {
		return
	}
	if g.hEvent != 0 {
		_ = windows.CloseHandle(g.hEvent)
		g.hEvent = 0
	}
	if g.hMutex != 0 {
		_ = windows.ReleaseMutex(g.hMutex)
		_ = windows.CloseHandle(g.hMutex)
		g.hMutex = 0
	}
}
