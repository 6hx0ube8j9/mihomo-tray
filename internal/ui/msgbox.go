package ui

import (
	"fmt"
	"reflect"
	"runtime"
	"sync"
	"syscall"

	"github.com/tailscale/walk"
	"github.com/tailscale/win"
)

type cbtHookContext struct {
	hHook      uintptr
	targetHWND win.HWND
}

var (
	cbtHookMu      sync.Mutex
	cbtHookMap     = make(map[uint32]*cbtHookContext)
	globalCallback uintptr
)

func init() {
	globalCallback = syscall.NewCallback(cbtHookProc)
}

func cbtHookProc(nCode int32, wParam uintptr, lParam uintptr) uintptr {
	tid := win.GetCurrentThreadId()

	cbtHookMu.Lock()
	ctx := cbtHookMap[tid]
	cbtHookMu.Unlock()

	if ctx != nil && nCode == hcbtActivate {
		msgBoxHwnd := win.HWND(wParam)

		x, y := calcCenteredPos(ctx.targetHWND, msgBoxHwnd)
		win.SetWindowPos(msgBoxHwnd, 0, x, y, 0, 0, win.SWP_NOSIZE|win.SWP_NOZORDER|win.SWP_NOACTIVATE)

		if ctx.hHook != 0 {
			procUnhookWindowsHookEx.Call(ctx.hHook)
			ctx.hHook = 0
		}
	}

	var hHook uintptr
	if ctx != nil {
		hHook = ctx.hHook
	}
	ret, _, _ := procCallNextHookEx.Call(hHook, uintptr(nCode), wParam, lParam)
	return ret
}

func ShowConfirm(target any, title, message string) bool {
	res := show(target, title, message, walk.MsgBoxYesNo|walk.MsgBoxIconQuestion)
	return res == win.IDYES
}

func ShowError(target any, title string, errOrMsg any) {
	show(target, title, toMessage(errOrMsg), walk.MsgBoxOK|walk.MsgBoxIconError)
}

func ShowWarning(target any, title, message string) {
	show(target, title, message, walk.MsgBoxOK|walk.MsgBoxIconWarning)
}

func ShowInfo(target any, title, message string) {
	show(target, title, message, walk.MsgBoxOK|walk.MsgBoxIconInformation)
}

func show(target any, title, message string, style walk.MsgBoxStyle) int {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	
	tid := win.GetCurrentThreadId()

	targetHWND, ownerForm := resolveTarget(target)
	ctx := &cbtHookContext{targetHWND: targetHWND}

	cbtHookMu.Lock()
	cbtHookMap[tid] = ctx
	cbtHookMu.Unlock()

	defer func() {
		cbtHookMu.Lock()
		delete(cbtHookMap, tid)
		cbtHookMu.Unlock()
	}()

	ctx.hHook, _, _ = procSetWindowsHookExW.Call(uintptr(whCBT), globalCallback, 0, uintptr(tid))
	defer func() {
		if ctx.hHook != 0 {
			procUnhookWindowsHookEx.Call(ctx.hHook)
			ctx.hHook = 0
		}
	}()

	formattedMsg := message 
	
	return walk.MsgBox(ownerForm, title, formattedMsg, style)
}

func resolveTarget(target any) (win.HWND, walk.Form) {
	if target == nil {
		return 0, nil
	}
	val := reflect.ValueOf(target)
	if val.Kind() == reflect.Ptr && val.IsNil() {
		return 0, nil
	}
	switch t := target.(type) {
	case walk.Form:
		return t.Handle(), t
	case walk.Widget:
		return t.Handle(), t.Form()
	case interface{ Handle() win.HWND }:
		return t.Handle(), nil
	}
	return 0, nil
}

func toMessage(v any) string {
	switch val := v.(type) {
	case error:
		if val != nil {
			return val.Error()
		}
		return "未知错误"
	case string:
		return val
	default:
		return fmt.Sprintf("%v", val)
	}
}
