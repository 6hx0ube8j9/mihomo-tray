package sys

import (
	"context"
	"errors"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procAllowSetForegroundWnd = modUser32.NewProc("AllowSetForegroundWindow")
)

type SingleInstanceGuard struct {
	hMutex  windows.Handle
	hEvent  windows.Handle
	hCancel windows.Handle
	hMapPid windows.Handle
}

func getSecAttr() *windows.SecurityAttributes {
	sd, err := windows.SecurityDescriptorFromString("D:(A;;GA;;;WD)S:(ML;;NW;;;LW)")
	if err != nil {
		return nil
	}
	var sa windows.SecurityAttributes
	sa.Length = uint32(unsafe.Sizeof(sa))
	sa.SecurityDescriptor = sd
	return &sa
}

func TryAcquireSingleInstance(mutexName, eventName string, _ uint32) (*SingleInstanceGuard, bool) {
	sa := getSecAttr()
	mName := windows.StringToUTF16Ptr(mutexName)

	hMutex, err := windows.CreateMutex(sa, true, mName)
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) || errors.Is(err, windows.ERROR_ACCESS_DENIED) || hMutex == 0 {
		if hMutex != 0 {
			_ = windows.CloseHandle(hMutex)
		}
		return nil, false
	}

	hEvent, _ := windows.CreateEvent(sa, 0, 0, windows.StringToUTF16Ptr(eventName))
	hCancel, _ := windows.CreateEvent(nil, 0, 0, nil)

	// 共享内存记录主进程 PID
	mapName := windows.StringToUTF16Ptr(eventName + "_PID")
	hMap, _ := windows.CreateFileMapping(windows.InvalidHandle, sa, windows.PAGE_READWRITE, 0, 4, mapName)
	if hMap != 0 {
		if addr, err := windows.MapViewOfFile(hMap, windows.FILE_MAP_WRITE, 0, 0, 4); err == nil && addr != 0 {
			*(*uint32)(unsafe.Pointer(addr)) = uint32(os.Getpid())
			_ = windows.UnmapViewOfFile(addr)
		}
	}

	return &SingleInstanceGuard{
		hMutex:  hMutex,
		hEvent:  hEvent,
		hCancel: hCancel,
		hMapPid: hMap,
	}, true
}

func NotifyExistingInstance(eventName string) {
	// 1. 读取主进程 PID 并过户前台控制权
	mapName := windows.StringToUTF16Ptr(eventName + "_PID")
	if hMap, _ := windows.CreateFileMapping(windows.InvalidHandle, nil, windows.PAGE_READWRITE, 0, 4, mapName); hMap != 0 {
		if addr, err := windows.MapViewOfFile(hMap, windows.FILE_MAP_READ, 0, 0, 4); err == nil && addr != 0 {
			targetPID := *(*uint32)(unsafe.Pointer(addr))
			_ = windows.UnmapViewOfFile(addr)
			if targetPID > 0 {
				_, _, _ = procAllowSetForegroundWnd.Call(uintptr(targetPID))
			}
		}
		_ = windows.CloseHandle(hMap)
	}

	// 2. 唤醒主进程
	if hEvent, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, windows.StringToUTF16Ptr(eventName)); err == nil && hEvent != 0 {
		_ = windows.SetEvent(hEvent)
		_ = windows.CloseHandle(hEvent)
	}
}

func (g *SingleInstanceGuard) ListenWakeEvent(ctx context.Context, onWake func()) {
	if g == nil || g.hEvent == 0 || g.hCancel == 0 {
		return
	}
	stop := context.AfterFunc(ctx, func() { _ = windows.SetEvent(g.hCancel) })
	go func() {
		defer stop()
		handles := [2]windows.Handle{g.hEvent, g.hCancel}
		for {
			ev, err := windows.WaitForMultipleObjects(handles[:], false, windows.INFINITE)
			if err != nil || ev != windows.WAIT_OBJECT_0 {
				return
			}
			if ctx.Err() == nil {
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
