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
	activeHookId win.HHOOK
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
									if isCtrl {
										ctx.dlg.Accept()
										pMsg.Message = win.WM_NULL
										return 0
									}
									break // Allow multiline TextEdit to handle Enter natively
								} else {
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
					if isCtrl {
						ctx.dlg.Accept()
						pMsg.Message = win.WM_NULL
						return 0
					}
				}
			}
		}
	}

	return win.CallNextHookEx(0, nCode, wParam, lParam)
}

// CollectInputs recursively traverses the container to find all LineEdit and TextEdit controls.
func CollectInputs(container walk.Container) []walk.Widget {
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
func FocusFirstInput(inputs []walk.Widget) {
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
// Returns a cleanup closure that must be called via defer before dialog disposal.
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
			if style&win.ES_WANTRETURN == 0 {
				win.SetWindowLong(hwnd, win.GWL_STYLE, style|win.ES_WANTRETURN)
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
		activeHookId = win.SetWindowsHookEx(win.WH_GETMESSAGE, kfCallback, 0, tid)
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
			win.UnhookWindowsHookEx(activeHookId)
			activeHookId = 0
		}
		kfStackMu.Unlock()

		runtime.UnlockOSThread()
	}
}
