package ui

import (
	"fmt"
	"log/slog"
	"runtime"
	"sync"
	"syscall"

	"github.com/tailscale/walk"
	"github.com/tailscale/win"

	"mihomo-tray/internal/sys"
)

const dialogWindowClass = "#32770"

type cbtHookContext struct {
	hHook       uintptr
	targetHWND  win.HWND
	onActivated func(hwnd win.HWND)
	centered    bool
}

var (
	cbtHookMu      sync.Mutex
	cbtHookMap     = make(map[uint32][]*cbtHookContext)
	cbtOnce        sync.Once
	globalCallback uintptr
)

func ensureCBTHookCallback() {
	cbtOnce.Do(func() {
		globalCallback = syscall.NewCallback(cbtHookProc)
	})
}

func cbtHookProc(nCode, wParam, lParam uintptr) uintptr {
	tid := win.GetCurrentThreadId()

	cbtHookMu.Lock()
	var ctx *cbtHookContext
	if stack := cbtHookMap[tid]; len(stack) > 0 {
		ctx = stack[len(stack)-1]
	}
	cbtHookMu.Unlock()

	if ctx != nil && int32(nCode) == hcbtActivate && !ctx.centered {
		msgBoxHwnd := win.HWND(wParam)
		if msgBoxHwnd != 0 &&
			msgBoxHwnd != ctx.targetHWND &&
			sys.GetWindowClassName(uintptr(msgBoxHwnd)) == dialogWindowClass {
			ctx.centered = true
			sys.CenterWindowOnOwner(uintptr(msgBoxHwnd), uintptr(ctx.targetHWND))

			if ctx.onActivated != nil {
				ctx.onActivated(msgBoxHwnd)
			}
		}
	}

	var hHook uintptr
	if ctx != nil {
		hHook = ctx.hHook
	}
	ret, _, _ := procCallNextHookEx.Call(hHook, nCode, wParam, lParam)
	return ret
}

func ShowNativeMsgBox(owner walk.Form, title, message string, style walk.MsgBoxStyle, onActivated func(hwnd win.HWND)) int {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	tid := win.GetCurrentThreadId()

	var targetHWND win.HWND
	var effectiveOwner walk.Form

	if owner != nil {
		ownerHWND := owner.Handle()
		if ownerHWND != 0 && win.IsWindowVisible(ownerHWND) && !win.IsIconic(ownerHWND) {
			targetHWND = ownerHWND

			hActive := win.GetForegroundWindow()
			if hActive == ownerHWND || win.IsChild(ownerHWND, hActive) {
				effectiveOwner = owner
			}
		}
	}

	if effectiveOwner == nil {
		style |= walk.MsgBoxStyle(mbSetForeground | mbTopMost)
	}

	ctx := &cbtHookContext{
		targetHWND:  targetHWND,
		onActivated: onActivated,
	}

	cbtHookMu.Lock()
	cbtHookMap[tid] = append(cbtHookMap[tid], ctx)
	cbtHookMu.Unlock()

	defer func() {
		cbtHookMu.Lock()
		stack := cbtHookMap[tid]
		for i := len(stack) - 1; i >= 0; i-- {
			if stack[i] == ctx {
				stack = append(stack[:i], stack[i+1:]...)
				break
			}
		}
		if len(stack) == 0 {
			delete(cbtHookMap, tid)
		} else {
			cbtHookMap[tid] = stack
		}
		cbtHookMu.Unlock()
	}()

	ensureCBTHookCallback()

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
