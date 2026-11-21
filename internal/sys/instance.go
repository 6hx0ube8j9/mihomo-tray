//go:build windows

package sys

import (
	"context"
	"log/slog"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const restartingProcessWaitTimeout = 3 * time.Second

type SingleInstanceGuard struct {
	mu         sync.Mutex
	hMutex     windows.Handle
	hEvent     windows.Handle
	hCancel    windows.Handle
	hAck       windows.Handle
	closed     bool
	listenDone chan struct{}
	afterDone  chan struct{}
	stopAfter  func() bool

	listenOnce sync.Once
	closeOnce  sync.Once
}

func getPermissiveSecAttr() *windows.SecurityAttributes {
	sd, err := windows.SecurityDescriptorFromString("D:(A;;GA;;;WD)S:(ML;;NW;;;LW)")
	if err != nil {
		slog.Warn("初始化通信权限失败", "err", err)
		return nil
	}
	var sa windows.SecurityAttributes
	sa.Length = uint32(unsafe.Sizeof(sa))
	sa.SecurityDescriptor = sd
	return &sa
}

func TryAcquireSingleInstance(mutexName, eventName string, oldPID uint32) (*SingleInstanceGuard, bool) {
	sa := getPermissiveSecAttr()
	mName, err := windows.UTF16PtrFromString(mutexName)
	if err != nil {
		return nil, false
	}

	hMutex, err := windows.CreateMutex(sa, true, mName)
	if err == nil && hMutex != 0 {
		return buildGuard(sa, eventName, hMutex)
	}

	if oldPID == 0 {
		if hMutex != 0 {
			_ = windows.CloseHandle(hMutex)
		}
		return nil, false
	}

	if oldPID > 1 {
		waitForOldProcessHandle(oldPID, 2*time.Second)
	}

	if hMutex != 0 {
		waitStatus, waitErr := windows.WaitForSingleObject(hMutex, uint32(restartingProcessWaitTimeout.Milliseconds()))
		if waitErr == nil && (waitStatus == windows.WAIT_OBJECT_0 || waitStatus == windows.WAIT_ABANDONED) {
			return buildGuard(sa, eventName, hMutex)
		}
		_ = windows.CloseHandle(hMutex)
	}

	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)

		retryMutex, retryErr := windows.CreateMutex(sa, true, mName)
		if retryErr == nil && retryMutex != 0 {
			return buildGuard(sa, eventName, retryMutex)
		}
		if retryMutex != 0 {
			_ = windows.CloseHandle(retryMutex)
		}
	}

	return nil, false
}

func buildGuard(sa *windows.SecurityAttributes, eventName string, hMutex windows.Handle) (*SingleInstanceGuard, bool) {
	eName, _ := windows.UTF16PtrFromString(eventName)
	hEvent, _ := windows.CreateEvent(sa, 0, 0, eName)
	hCancel, _ := windows.CreateEvent(nil, 1, 0, nil)

	ackName, _ := windows.UTF16PtrFromString(eventName + "_ACK")
	hAck, _ := windows.CreateEvent(sa, 0, 0, ackName)

	return &SingleInstanceGuard{
		hMutex:  hMutex,
		hEvent:  hEvent,
		hCancel: hCancel,
		hAck:    hAck,
	}, true
}

func waitForOldProcessHandle(pid uint32, timeout time.Duration) {
	hProcess, err := windows.OpenProcess(windows.SYNCHRONIZE, false, pid)
	if err != nil {
		return
	}
	defer windows.CloseHandle(hProcess)

	_, _ = windows.WaitForSingleObject(hProcess, uint32(timeout.Milliseconds()))
}

func NotifyExistingInstance(eventName string) {
	eName, err := windows.UTF16PtrFromString(eventName)
	if err != nil {
		return
	}
	ackName, err := windows.UTF16PtrFromString(eventName + "_ACK")
	if err != nil {
		return
	}

	hAck, errAck := windows.OpenEvent(windows.EVENT_MODIFY_STATE|windows.SYNCHRONIZE, false, ackName)
	if errAck == nil && hAck != 0 {
		_ = windows.ResetEvent(hAck)
		defer windows.CloseHandle(hAck)
	}

	hEvent, errEvt := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, eName)
	if errEvt != nil || hEvent == 0 {
		return
	}

	AllowSetForegroundWindow(asfwAny)
	_ = windows.SetEvent(hEvent)
	_ = windows.CloseHandle(hEvent)

	if hAck != 0 {
		_, _ = windows.WaitForSingleObject(hAck, 2000)
	}
}

func (g *SingleInstanceGuard) ListenWakeEvent(ctx context.Context, onWake func()) {
	if g == nil || onWake == nil {
		return
	}

	g.listenOnce.Do(func() {
		g.mu.Lock()
		if g.closed || g.hEvent == 0 || g.hCancel == 0 {
			g.mu.Unlock()
			return
		}

		hEvent := g.hEvent
		hCancel := g.hCancel
		listenDone := make(chan struct{})
		afterDone := make(chan struct{})
		g.listenDone = listenDone
		g.afterDone = afterDone
		g.stopAfter = context.AfterFunc(ctx, func() {
			defer close(afterDone)
			g.mu.Lock()
			if !g.closed && g.hCancel != 0 {
				_ = windows.SetEvent(g.hCancel)
			}
			g.mu.Unlock()
		})
		g.mu.Unlock()

		go func() {
			defer close(listenDone)
			handles := [2]windows.Handle{hEvent, hCancel}

			for {
				event, err := windows.WaitForMultipleObjects(handles[:], false, windows.INFINITE)
				if err != nil || event != windows.WAIT_OBJECT_0 {
					return
				}

				if ctx.Err() == nil {
					slog.Debug("收到唤醒面板请求")
					onWake()
				}
			}
		}()
	})
}

func (g *SingleInstanceGuard) AckWake() {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.closed && g.hAck != 0 {
		_ = windows.SetEvent(g.hAck)
	}
}

func (g *SingleInstanceGuard) Close() {
	if g == nil {
		return
	}

	g.closeOnce.Do(func() {
		g.mu.Lock()
		g.closed = true
		if g.hCancel != 0 {
			_ = windows.SetEvent(g.hCancel)
		}
		listenDone := g.listenDone
		afterDone := g.afterDone
		stopAfter := g.stopAfter
		g.mu.Unlock()

		if stopAfter != nil && !stopAfter() && afterDone != nil {
			<-afterDone
		}
		if listenDone != nil {
			<-listenDone
		}

		g.mu.Lock()
		defer g.mu.Unlock()

		if g.hCancel != 0 {
			_ = windows.CloseHandle(g.hCancel)
			g.hCancel = 0
		}
		if g.hEvent != 0 {
			_ = windows.CloseHandle(g.hEvent)
			g.hEvent = 0
		}
		if g.hAck != 0 {
			_ = windows.CloseHandle(g.hAck)
			g.hAck = 0
		}
		if g.hMutex != 0 {
			_ = windows.ReleaseMutex(g.hMutex)
			_ = windows.CloseHandle(g.hMutex)
			g.hMutex = 0
		}
	})
}