package ui

import (
	"unsafe"

	"github.com/tailscale/walk"
	"github.com/tailscale/win"
)

func ActivateWindow(hwnd win.HWND) {
	if hwnd == 0 || !win.IsWindowVisible(hwnd) {
		return
	}
	if win.IsIconic(hwnd) {
		win.ShowWindow(hwnd, win.SW_RESTORE)
	}
	win.SetForegroundWindow(hwnd)
	win.SetFocus(hwnd)
}

func calcCenteredPos(targetHWND, popupHWND win.HWND) (x, y int32) {
	var popRect win.RECT
	win.GetWindowRect(popupHWND, &popRect)
	dlgW := popRect.Right - popRect.Left
	dlgH := popRect.Bottom - popRect.Top

	var workArea win.RECT
	win.SystemParametersInfo(0x0030, 0, unsafe.Pointer(&workArea), 0)

	if targetHWND != 0 && win.IsWindowVisible(targetHWND) && !win.IsIconic(targetHWND) {
		var clientRect win.RECT
		win.GetClientRect(targetHWND, &clientRect)
		pt := win.POINT{X: 0, Y: 0}
		win.ClientToScreen(targetHWND, &pt)

		centerCX := pt.X + clientRect.Right/2
		centerCY := pt.Y + clientRect.Bottom/2

		x = centerCX - dlgW/2
		y = centerCY - dlgH/2
	} else {
		screenW := workArea.Right - workArea.Left
		screenH := workArea.Bottom - workArea.Top
		x = workArea.Left + (screenW-dlgW)/2
		y = workArea.Top + (screenH-dlgH)/2
	}

	if x < workArea.Left {
		x = workArea.Left
	} else if x+dlgW > workArea.Right {
		x = workArea.Right - dlgW
	}

	if y < workArea.Top {
		y = workArea.Top
	} else if y+dlgH > workArea.Bottom {
		y = workArea.Bottom - dlgH
	}

	return x, y
}

func centerDialog(dlg *walk.Dialog, owner walk.Form) {
	if dlg == nil {
		return
	}

	var targetHWND win.HWND
	if owner != nil && owner.Visible() && !win.IsIconic(owner.Handle()) {
		targetHWND = owner.Handle()
	}

	x, y := calcCenteredPos(targetHWND, dlg.Handle())
	win.SetWindowPos(dlg.Handle(), win.HWND_TOP, x, y, 0, 0, win.SWP_NOSIZE)
}

func lockWindowSize(hwnd win.HWND) {
	style := win.GetWindowLong(hwnd, win.GWL_STYLE)
	style &^= win.WS_THICKFRAME | win.WS_MAXIMIZEBOX
	win.SetWindowLong(hwnd, win.GWL_STYLE, style)
	win.SetWindowPos(hwnd, 0, 0, 0, 0, 0, win.SWP_NOMOVE|win.SWP_NOSIZE|win.SWP_NOZORDER|win.SWP_FRAMECHANGED)
}

func restoreFocus(parent walk.Form, hActive win.HWND) {
	if parent != nil && parent.Visible() && !win.IsIconic(parent.Handle()) {
		win.SetForegroundWindow(parent.Handle())
		win.SetFocus(parent.Handle())
	} else if hActive != 0 && win.IsWindowVisible(hActive) && !win.IsIconic(hActive) {
		win.SetForegroundWindow(hActive)
		win.SetFocus(hActive)
	}
}


// Idempotent action helpers to prevent redundant Win32 redraws.
func safelySetChecked(act *walk.Action, checked bool) {
	if act != nil && act.Checked() != checked {
		act.SetChecked(checked)
	}
}

func safelySetEnabled(act *walk.Action, enabled bool) {
	if act != nil && act.Enabled() != enabled {
		act.SetEnabled(enabled)
	}
}

func safelySetText(act *walk.Action, text string) {
	if act != nil && act.Text() != text {
		act.SetText(text)
	}
}
