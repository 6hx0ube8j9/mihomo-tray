package ui

import (
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"github.com/tailscale/walk"
	"github.com/tailscale/win"
)

var (
	kfOnce       sync.Once
	kfCallback   uintptr
	kfStackMu    sync.Mutex
	kfStack      []*keyFlowContext
	activeHookId uintptr
)

type keyFlowContext struct {
	dlg        *walk.Dialog
	acceptHWND win.HWND
	cancelHWND win.HWND
	inputHWNDs []win.HWND
	isTextEdit []bool
}

func ensureKeyFlowCallback() {
	kfOnce.Do(func() {
		kfCallback = syscall.NewCallback(keyFlowMessageProc)
	})
}

// keyFlowMessageProc intercepts raw key messages before IsDialogMessage processing.
func keyFlowMessageProc(nCode int32, wParam uintptr, lParam uintptr) uintptr {
	if nCode >= 0 {
		kfStackMu.Lock()
		var ctx *keyFlowContext
		if len(kfStack) > 0 {
			ctx = kfStack[len(kfStack)-1] // Always route to top active dialog
		}
		kfStackMu.Unlock()

		if ctx != nil {
			pMsg := (*win.MSG)(unsafe.Pointer(lParam))
			if pMsg.Message == win.WM_KEYDOWN {
				isCtrl := win.GetKeyState(win.VK_CONTROL) < 0
				hFocus := win.GetFocus()

				switch pMsg.WParam {
				case win.VK_ESCAPE:
					ctx.dlg.Cancel()
					pMsg.Message = win.WM_NULL
					return 0

				case win.VK_RETURN:
					switch {
					case hFocus == ctx.acceptHWND:
						ctx.dlg.Accept()
						pMsg.Message = win.WM_NULL
						return 0

					case hFocus == ctx.cancelHWND:
						ctx.dlg.Cancel()
						pMsg.Message = win.WM_NULL
						return 0

					default:
						for i, hwnd := range ctx.inputHWNDs {
							if hFocus == hwnd {
								if ctx.isTextEdit[i] {
									// TextEdit: Ctrl+Enter saves; plain Enter inserts newline
									if isCtrl {
										ctx.dlg.Accept()
										pMsg.Message = win.WM_NULL
										return 0
									}
									break
								} else {
									// LineEdit: Ctrl+Enter saves; plain Enter shifts focus to next input
									if isCtrl {
										ctx.dlg.Accept()
										pMsg.Message = win.WM_NULL
										return 0
									}
									if i+1 < len(ctx.inputHWNDs) {
										win.SetFocus(ctx.inputHWNDs[i+1])
									} else {
										win.SetFocus(ctx.acceptHWND)
									}
									pMsg.Message = win.WM_NULL
									return 0
								}
							}
						}
					}

				case 'S':
					// Ctrl+S: shortcut to save anywhere in the dialog
					if isCtrl {
						ctx.dlg.Accept()
						pMsg.Message = win.WM_NULL
						return 0
					}
				}
			}
		}
	}

	ret, _, _ := procCallNextHookEx.Call(0, uintptr(nCode), wParam, lParam)
	return ret
}

// CollectInputs recursively traverses the container to find all LineEdit and TextEdit controls.
func collectInputs(container walk.Container) []walk.Widget {
	if container == nil || container.Children() == nil {
		return nil
	}
	var list []walk.Widget
	for i := 0; i < container.Children().Len(); i++ {
		child := container.Children().At(i)
		switch w := child.(type) {
		case *walk.LineEdit, *walk.TextEdit:
			list = append(list, w)
		case walk.Container:
			list = append(list, CollectInputs(w)...)
		}
	}
	return list
}

// FocusFirstInput focuses the first editable input and positions the caret at the end.
func focusFirstInput(inputs []walk.Widget) {
	for _, in := range inputs {
		if in.Visible() && in.Enabled() {
			in.SetFocus()
			if le, ok := in.(*walk.LineEdit); ok {
				textLen := len([]rune(le.Text()))
				le.SetTextSelection(textLen, textLen)
			}
			return
		}
	}
}

// SetupDialogKeyFlow sets up input traversal, auto-wrap styles, initial focus, and keyboard routing.
func SetupDialogKeyFlow(dlg *walk.Dialog, acceptPB, cancelPB *walk.PushButton) func() {
	ensureKeyFlowCallback()

	inputs := CollectInputs(dlg)
	var inputHWNDs []win.HWND
	var isTextEdit []bool

	for _, in := range inputs {
		inputHWNDs = append(inputHWNDs, in.Handle())
		_, ok := in.(*walk.TextEdit)
		isTextEdit = append(isTextEdit, ok)

		if ok {
			hwnd := in.Handle()
			style := win.GetWindowLong(hwnd, win.GWL_STYLE)
			if style&esWantReturn == 0 {
				win.SetWindowLong(hwnd, win.GWL_STYLE, style|esWantReturn)
				win.SetWindowPos(hwnd, 0, 0, 0, 0, 0, win.SWP_NOMOVE|win.SWP_NOSIZE|win.SWP_NOZORDER|win.SWP_FRAMECHANGED)
			}
		}
	}

	dlg.Activating().Attach(func() {
		FocusFirstInput(inputs)
	})

	ctx := &keyFlowContext{
		dlg:        dlg,
		acceptHWND: acceptPB.Handle(),
		cancelHWND: cancelPB.Handle(),
		inputHWNDs: inputHWNDs,
		isTextEdit: isTextEdit,
	}

	runtime.LockOSThread()

	kfStackMu.Lock()
	if len(kfStack) == 0 {
		tid := win.GetCurrentThreadId()
		hHook, _, _ := procSetWindowsHookExW.Call(uintptr(whGetMessage), kfCallback, 0, uintptr(tid))
		activeHookId = hHook
	}
	kfStack = append(kfStack, ctx)
	kfStackMu.Unlock()

	return func() {
		kfStackMu.Lock()
		for i := len(kfStack) - 1; i >= 0; i-- {
			if kfStack[i] == ctx {
				kfStack = append(kfStack[:i], kfStack[i+1:]...)
				break
			}
		}
		if len(kfStack) == 0 && activeHookId != 0 {
			procUnhookWindowsHookEx.Call(activeHookId)
			activeHookId = 0
		}
		kfStackMu.Unlock()

		runtime.UnlockOSThread()
	}
}
