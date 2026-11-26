//go:build windows

package sys

import (
	"fmt"
	"runtime"
	"syscall"
	"time"
	"unsafe"
)

var (
	procOpenClipboard    = modUser32.NewProc("OpenClipboard")
	procCloseClipboard   = modUser32.NewProc("CloseClipboard")
	procEmptyClipboard   = modUser32.NewProc("EmptyClipboard")
	procSetClipboardData = modUser32.NewProc("SetClipboardData")

	procGlobalAlloc   = modKernel32.NewProc("GlobalAlloc")
	procGlobalFree    = modKernel32.NewProc("GlobalFree")
	procGlobalLock    = modKernel32.NewProc("GlobalLock")
	procGlobalUnlock  = modKernel32.NewProc("GlobalUnlock")
	procRtlMoveMemory = modKernel32.NewProc("RtlMoveMemory")
)

const (
	cfUnicodeText = 13     // CF_UNICODETEXT
	gmemMoveable  = 0x0002 // GMEM_MOVEABLE
)

func WriteToClipboard(text string) error {
	if text == "" {
		return nil
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	hMem, err := makeClipboardBuffer(text)
	if err != nil {
		return fmt.Errorf("构造剪贴板数据失败: %w", err)
	}

	if err := openClipboardWithRetry(8, 15*time.Millisecond); err != nil {
		procGlobalFree.Call(hMem)
		return err
	}
	defer procCloseClipboard.Call()

	if r, _, callErr := procEmptyClipboard.Call(); r == 0 {
		procGlobalFree.Call(hMem)
		return fmt.Errorf("清空剪贴板失败: %w", callErr)
	}

	if r, _, callErr := procSetClipboardData.Call(uintptr(cfUnicodeText), hMem); r == 0 {
		procGlobalFree.Call(hMem)
		return fmt.Errorf("写入剪贴板数据失败: %w", callErr)
	}

	return nil
}

func makeClipboardBuffer(text string) (uintptr, error) {
	u16, err := syscall.UTF16FromString(text)
	if err != nil {
		return 0, err
	}

	size := uintptr(len(u16) * 2)
	hMem, _, callErr := procGlobalAlloc.Call(gmemMoveable, size)
	if hMem == 0 {
		return 0, fmt.Errorf("GlobalAlloc 失败: %w", callErr)
	}

	ptr, _, callErr := procGlobalLock.Call(hMem)
	if ptr == 0 {
		procGlobalFree.Call(hMem)
		return 0, fmt.Errorf("GlobalLock 失败: %w", callErr)
	}
	defer procGlobalUnlock.Call(hMem)

	_, _, _ = procRtlMoveMemory.Call(ptr, uintptr(unsafe.Pointer(&u16[0])), size)
	runtime.KeepAlive(u16)
	return hMem, nil	
}

func openClipboardWithRetry(attempts int, interval time.Duration) error {
	var lastErr error
	for i := 0; i < attempts; i++ {
		r, _, err := procOpenClipboard.Call(0)
		if r != 0 {
			return nil
		}
		lastErr = err
		if i < attempts-1 {
			time.Sleep(interval)
		}
	}
	return fmt.Errorf("打开系统剪贴板超时: %w", lastErr)
}
