package ui

import (
	"unsafe"

	"github.com/tailscale/walk"
	"github.com/tailscale/win"
)

func ActivateWindow(hwnd win.HWND) {
}

// centerDialog 已空心化。
func centerDialog(dlg *walk.Dialog, owner walk.Form) {
}


func lockWindowSize(hwnd win.HWND) {
}


func restoreFocus(parent walk.Form, hActive win.HWND) {
}


func calcCenteredPos(targetHWND, popupHWND win.HWND) (x, y int32) {
	var popRect win.RECT
	win.GetWindowRect(popupHWND, &popRect)
	dlgW := popRect.Right - popRect.Left
	dlgH := popRect.Bottom - popRect.Top

	if targetHWND != 0 && win.IsWindowVisible(targetHWND) && !win.IsIconic(targetHWND) {
		var clientRect win.RECT
		win.GetClientRect(targetHWND, &clientRect)
		pt := win.POINT{X: 0, Y: 0}
		win.ClientToScreen(targetHWND, &pt)

		centerCX := pt.X + clientRect.Right/2
		centerCY := pt.Y + clientRect.Bottom/2

		return centerCX - dlgW/2, centerCY - dlgH/2
	}

	var workArea win.RECT
	win.SystemParametersInfo(0x0030, 0, unsafe.Pointer(&workArea), 0)

	screenW := workArea.Right - workArea.Left
	screenH := workArea.Bottom - workArea.Top
	x = workArea.Left + (screenW-dlgW)/2
	y = workArea.Top + (screenH-dlgH)/2

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
