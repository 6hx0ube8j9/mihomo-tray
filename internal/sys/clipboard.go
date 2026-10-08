package sys

import (
	"errors"
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

	procGlobalAlloc  = modKernel32.NewProc("GlobalAlloc")
	procGlobalFree   = modKernel32.NewProc("GlobalFree")
	procGlobalLock   = modKernel32.NewProc("GlobalLock")
	procGlobalUnlock = modKernel32.NewProc("GlobalUnlock")
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

	procEmptyClipboard.Call()
	if r, _, _ := procSetClipboardData.Call(uintptr(cfUnicodeText), hMem); r == 0 {
		procGlobalFree.Call(hMem)
		return errors.New("交付剪贴板数据失败")
	}

	return nil
}

func makeClipboardBuffer(text string) (uintptr, error) {
	u16, err := syscall.UTF16FromString(text)
	if err != nil {
		return 0, err
	}

	size := uintptr(len(u16) * 2)
	hMem, _, _ := procGlobalAlloc.Call(gmemMoveable, size)
	if hMem == 0 {
		return 0, errors.New("GlobalAlloc 失败")
	}

	ptr, _, _ := procGlobalLock.Call(hMem)
	if ptr == 0 {
		procGlobalFree.Call(hMem)
		return 0, errors.New("GlobalLock 失败")
	}
	defer procGlobalUnlock.Call(hMem)

	dest := unsafe.Slice((*uint16)(unsafe.Pointer(ptr)), len(u16))
	copy(dest, u16)

	return hMem, nil
}

func openClipboardWithRetry(attempts int, interval time.Duration) error {
	for i := 0; i < attempts; i++ {
		if r, _, _ := procOpenClipboard.Call(0); r != 0 {
			return nil
		}
		time.Sleep(interval)
	}
	return errors.New("打开系统剪贴板超时 (句柄被占用)")
}
