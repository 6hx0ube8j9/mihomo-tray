package ui

import (
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"github.com/tailscale/walk"
	"github.com/tailscale/win"
)

const (
	bmClick           = 0x00F5     // Win32 BM_CLICK
	bmSetStyle        = 0x00F4     // Win32 BM_SETSTYLE
	bsDefPushButton   = 0x0001     // Win32 BS_DEFPUSHBUTTON
	wsExControlParent = 0x00010000 // WS_EX_CONTROLPARENT
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
	inputs     []walk.Widget
	isTextEdit []bool
}

func ensureKeyFlowCallback() {
	kfOnce.Do(func() {
		kfCallback = syscall.NewCallback(keyFlowMessageProc)
	})
}

func keyFlowMessageProc(nCode int32, wParam uintptr, lParam uintptr) uintptr {
	if nCode >= 0 && wParam == pmRemove {
		kfStackMu.Lock()
		var ctx *keyFlowContext
		if len(kfStack) > 0 {
			ctx = kfStack[len(kfStack)-1]
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
					if (pMsg.LParam & (1 << 30)) != 0 {
						pMsg.Message = win.WM_NULL
						return 0
					}

					switch {
					case hFocus == ctx.acceptHWND:
						win.SendMessage(ctx.acceptHWND, bmClick, 0, 0)
						pMsg.Message = win.WM_NULL
						return 0

					case ctx.cancelHWND != 0 && hFocus == ctx.cancelHWND:
						win.SendMessage(ctx.cancelHWND, bmClick, 0, 0)
						pMsg.Message = win.WM_NULL
						return 0

					default:
						for i, hwnd := range ctx.inputHWNDs {
							if hFocus == hwnd || win.IsChild(hwnd, hFocus) {
								if ctx.isTextEdit[i] {
									if isCtrl {
										win.SendMessage(ctx.acceptHWND, bmClick, 0, 0)
										pMsg.Message = win.WM_NULL
										return 0
									}
									break
								} else {
									if isCtrl {
										win.SendMessage(ctx.acceptHWND, bmClick, 0, 0)
										pMsg.Message = win.WM_NULL
										return 0
									}

									var next walk.Widget
									for j := i + 1; j < len(ctx.inputs); j++ {
										candidate := ctx.inputs[j]
										if candidate.Visible() && candidate.Enabled() {
											next = candidate
											break
										}
									}

									if next != nil {
										next.SetFocus()
										if nextLE, ok := next.(*walk.LineEdit); ok {
											l := len([]rune(nextLE.Text()))
											nextLE.SetTextSelection(0, l)
										}
									} else {
										win.SetFocus(ctx.acceptHWND)
										win.SendMessage(ctx.acceptHWND, bmSetStyle, uintptr(bsDefPushButton), 1)
									}
									pMsg.Message = win.WM_NULL
									return 0
								}
							}
						}
					}

				case 'S':
					if isCtrl {
						win.SendMessage(ctx.acceptHWND, bmClick, 0, 0)
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

func collectInputs(container walk.Container) []walk.Widget {
	if container == nil || container.Children() == nil {
		return nil
	}
	var list []walk.Widget
	for i := 0; i < container.Children().Len(); i++ {
		child := container.Children().At(i)
		switch w := child.(type) {
		case *walk.LineEdit, *walk.TextEdit, *walk.NumberEdit:
			list = append(list, w)
		case walk.Container:
			list = append(list, collectInputs(w)...)
		}
	}
	return list
}

func focusFirstInput(inputs []walk.Widget) {
	for _, in := range inputs {
		if in.Visible() && in.Enabled() {
			in.SetFocus()
			if le, ok := in.(*walk.LineEdit); ok {
				textLen := len([]rune(le.Text()))
				le.SetTextSelection(0, textLen)
			}
			return
		}
	}
}

func SetupDialogKeyFlow(dlg *walk.Dialog, acceptPB, cancelPB *walk.PushButton) func() {
	ensureKeyFlowCallback()
	normalizeWindowHierarchy(dlg)

	inputs := collectInputs(dlg)
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

	dlg.Starting().Attach(func() {
		dlg.Synchronize(func() {
			focusFirstInput(inputs)
		})
	})

	var cancelHWND win.HWND
	if cancelPB != nil {
		cancelHWND = cancelPB.Handle()
	}

	ctx := &keyFlowContext{
		dlg:        dlg,
		acceptHWND: acceptPB.Handle(),
		cancelHWND: cancelHWND,
		inputHWNDs: inputHWNDs,
		inputs:     inputs,
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

func normalizeWindowHierarchy(container walk.Container) {
	if container == nil || container.Children() == nil {
		return
	}

	children := container.Children()
	count := children.Len()

	for i := 0; i < count; i++ {
		child := children.At(i)
		hwnd := child.Handle()

		if _, ok := child.(walk.Container); ok {
			exStyle := win.GetWindowLong(hwnd, win.GWL_EXSTYLE)
			if exStyle&wsExControlParent == 0 {
				win.SetWindowLong(hwnd, win.GWL_EXSTYLE, exStyle|wsExControlParent)
			}
			if c, ok := child.(walk.Container); ok {
				normalizeWindowHierarchy(c)
			}
		}

		win.SetWindowPos(hwnd, win.HWND_BOTTOM, 0, 0, 0, 0, win.SWP_NOMOVE|win.SWP_NOSIZE)
	}
}
