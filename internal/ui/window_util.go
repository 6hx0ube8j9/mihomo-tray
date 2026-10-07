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

	hActive := win.GetForegroundWindow()
	targetIsActive := targetHWND != 0 && (hActive == targetHWND || win.IsChild(targetHWND, hActive))

	if targetHWND != 0 && win.IsWindowVisible(targetHWND) && !win.IsIconic(targetHWND) && targetIsActive {
		var tgtRect win.RECT
		win.GetWindowRect(targetHWND, &tgtRect)
		tgtW := tgtRect.Right - tgtRect.Left
		tgtH := tgtRect.Bottom - tgtRect.Top
		x = tgtRect.Left + (tgtW-dlgW)/2
		y = tgtRect.Top + (tgtH-dlgH)/2
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
