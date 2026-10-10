package ui

import (
	"fmt"
	"log/slog"
	"runtime"
	"sync"
	"syscall"

	"github.com/tailscale/walk"
	"github.com/tailscale/win"
)

type cbtHookContext struct {
	hHook       uintptr
	targetHWND  win.HWND
	onActivated func(hwnd win.HWND)
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

		if ctx.onActivated != nil {
			ctx.onActivated(msgBoxHwnd)
		}

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

func ShowNativeMsgBox(owner walk.Form, title, message string, style walk.MsgBoxStyle, onActivated func(hwnd win.HWND)) int {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	tid := win.GetCurrentThreadId()

	var targetHWND win.HWND
	var effectiveOwner walk.Form

	if owner != nil && owner.Visible() && !win.IsIconic(owner.Handle()) {
		hActive := win.GetForegroundWindow()
		if hActive == owner.Handle() || win.IsChild(owner.Handle(), hActive) {
			targetHWND = owner.Handle()
			effectiveOwner = owner
		}
	}

	ctx := &cbtHookContext{
		targetHWND:  targetHWND,
		onActivated: onActivated,
	}

	cbtHookMu.Lock()
	cbtHookMap[tid] = ctx
	cbtHookMu.Unlock()

	defer func() {
		cbtHookMu.Lock()
		delete(cbtHookMap, tid)
		cbtHookMu.Unlock()
	}()

	hHook, _, err := procSetWindowsHookExW.Call(uintptr(whCBT), globalCallback, 0, uintptr(tid))
	if hHook == 0 {
		slog.Warn("安装弹窗居中钩子失败", "tid", tid, "err", err)
	}
	ctx.hHook = hHook

	defer func() {
		if ctx.hHook != 0 {
			procUnhookWindowsHookEx.Call(ctx.hHook)
			ctx.hHook = 0
		}
	}()

	if style&walk.MsgBoxIconQuestion == walk.MsgBoxIconQuestion {
		win.MessageBeep(win.MB_ICONINFORMATION)
	}

	return walk.MsgBox(effectiveOwner, title, message, style)
}

func RunErrorDialog(owner walk.Form, title string, errOrMsg any) {
	ShowNativeMsgBox(owner, title, toMessage(errOrMsg), walk.MsgBoxOK|walk.MsgBoxIconError, nil)
}

func RunConfirmDialog(owner walk.Form, title, message string) bool {
	res := ShowNativeMsgBox(owner, title, message, walk.MsgBoxOKCancel|walk.MsgBoxIconQuestion, nil)
	return res == win.IDOK
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
