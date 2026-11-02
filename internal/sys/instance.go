// instance.go manages application single-instance enforcement, cross-integrity IPC
// wake-up signaling, and foreground privilege delegation.

package sys

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procAllowSetForegroundWnd = modUser32.NewProc("AllowSetForegroundWindow")
)

const (
	asfwAny                      = 0xFFFFFFFF
	restartingProcessWaitTimeout = 3 * time.Second
	waitObject0                  = 0x00000000
	waitAbandoned                = 0x00000080
)

type SingleInstanceGuard struct {
	hMutex  windows.Handle
	hEvent  windows.Handle
	hCancel windows.Handle
	hMapPid windows.Handle
}

func getPermissiveSecAttr() *windows.SecurityAttributes {
	sd, err := windows.SecurityDescriptorFromString("D:(A;;GA;;;WD)S:(ML;;NW;;;LW)")
	if err != nil {
		slog.Warn("创建安全描述符失败", "error", err)
		return nil
	}
	var sa windows.SecurityAttributes
	sa.Length = uint32(unsafe.Sizeof(sa))
	sa.SecurityDescriptor = sd
	return &sa
}

func TryAcquireSingleInstance(mutexName, eventName string, oldPID uint32) (*SingleInstanceGuard, bool) {
	sa := getPermissiveSecAttr()
	mName, _ := windows.UTF16PtrFromString(mutexName)
	isRestarting := oldPID > 0

	hMutex, err := windows.CreateMutex(sa, true, mName)
	isAlreadyExist := errors.Is(err, windows.ERROR_ALREADY_EXISTS) || errors.Is(err, windows.ERROR_ACCESS_DENIED)

	if !isAlreadyExist && err == nil && hMutex != 0 {
		return buildGuard(sa, eventName, hMutex)
	}

	if !isRestarting {
		if hMutex != 0 {
			_ = windows.CloseHandle(hMutex)
		}
		return nil, false
	}

	slog.Debug("等待旧进程释放单实例锁", "pid", oldPID)

	if hMutex != 0 {
		if oldPID > 1 {
			waitForOldProcessHandle(oldPID, 2*time.Second)
		}

		waitStatus, waitErr := windows.WaitForSingleObject(hMutex, uint32(restartingProcessWaitTimeout.Milliseconds()))
		if waitErr == nil && (waitStatus == waitObject0 || waitStatus == waitAbandoned) {
			slog.Info("已接管单实例锁")
			return buildGuard(sa, eventName, hMutex)
		}
		_ = windows.CloseHandle(hMutex)
	}

	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)

		retryMutex, retryErr := windows.CreateMutex(sa, true, mName)
		if !errors.Is(retryErr, windows.ERROR_ALREADY_EXISTS) && retryErr == nil && retryMutex != 0 {
			slog.Info("已接管单实例锁")
			return buildGuard(sa, eventName, retryMutex)
		}
		if retryMutex != 0 {
			_ = windows.CloseHandle(retryMutex)
		}
	}

	slog.Warn("等待旧进程释放单实例锁超时", "pid", oldPID)
	return nil, false
}

func buildGuard(sa *windows.SecurityAttributes, eventName string, hMutex windows.Handle) (*SingleInstanceGuard, bool) {
	eName, _ := windows.UTF16PtrFromString(eventName)
	hEvent, _ := windows.CreateEvent(sa, 0, 0, eName)
	hCancel, _ := windows.CreateEvent(nil, 0, 0, nil)

	// 在共享安全描述符下开辟 4 字节内存，写入主进程实际 PID
	hMapPid := storePID(sa, eventName, uint32(os.Getpid()))

	return &SingleInstanceGuard{
		hMutex:  hMutex,
		hEvent:  hEvent,
		hCancel: hCancel,
		hMapPid: hMapPid,
	}, true
}

func storePID(sa *windows.SecurityAttributes, baseName string, pid uint32) windows.Handle {
	mName, _ := windows.UTF16PtrFromString(baseName + "_PID")
	hMap, _ := windows.CreateFileMapping(windows.InvalidHandle, sa, windows.PAGE_READWRITE, 0, 4, mName)
	if hMap != 0 {
		addr, err := windows.MapViewOfFile(hMap, windows.FILE_MAP_WRITE, 0, 0, 4)
		if err == nil && addr != 0 {
			*(*uint32)(unsafe.Pointer(addr)) = pid
			_ = windows.UnmapViewOfFile(addr)
		}
	}
	return hMap
}

func readPID(baseName string) uint32 {
	mName, _ := windows.UTF16PtrFromString(baseName + "_PID")
	// Windows 原生机制：对已存在的命名共享内存调用 CreateFileMapping 会直接打开句柄，无需 OpenFileMapping
	hMap, _ := windows.CreateFileMapping(windows.InvalidHandle, nil, windows.PAGE_READWRITE, 0, 4, mName)
	if hMap == 0 || hMap == windows.InvalidHandle {
		return 0
	}
	defer windows.CloseHandle(hMap)

	addr, err := windows.MapViewOfFile(hMap, windows.FILE_MAP_READ, 0, 0, 4)
	if err != nil || addr == 0 {
		return 0
	}
	defer windows.UnmapViewOfFile(addr)

	return *(*uint32)(unsafe.Pointer(addr))
}

func waitForOldProcessHandle(pid uint32, timeout time.Duration) {
	hProcess, err := windows.OpenProcess(windows.SYNCHRONIZE, false, pid)
	if err != nil {
		return
	}
	defer windows.CloseHandle(hProcess)

	slog.Debug("等待旧进程退出", "pid", pid)
	_, _ = windows.WaitForSingleObject(hProcess, uint32(timeout.Milliseconds()))
}

func NotifyExistingInstance(eventName string) {
	eName, _ := windows.UTF16PtrFromString(eventName)
	hEvent, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, eName)
	if err == nil && hEvent != 0 {
		targetPID := readPID(eventName)
		if targetPID > 0 {
			_, _, _ = procAllowSetForegroundWnd.Call(uintptr(targetPID))
		} else {
			_, _, _ = procAllowSetForegroundWnd.Call(asfwAny)
		}

		_ = windows.SetEvent(hEvent)
		_ = windows.CloseHandle(hEvent)
		time.Sleep(50 * time.Millisecond)
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
				slog.Info("收到外部唤醒请求")
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
	if g.hMapPid != 0 {
		_ = windows.CloseHandle(g.hMapPid)
		g.hMapPid = 0
	}
	if g.hMutex != 0 {
		_ = windows.ReleaseMutex(g.hMutex)
		_ = windows.CloseHandle(g.hMutex)
		g.hMutex = 0
	}
}
