// Package ui provides window management helpers including dialog centering,
// screen bounds clamping, style locking, and focus restoration.
package ui

import (
	"github.com/tailscale/walk"
	"github.com/tailscale/win"

	"mihomo-tray/internal/sys"
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

func clampScreenPos(pos, minPos, maxPos int) int {
	if maxPos < minPos {
		return minPos
	}
	if pos < minPos {
		return minPos
	}
	if pos > maxPos {
		return maxPos
	}
	return pos
}

func calcCenteredPos(targetHWND, popupHWND win.HWND) (x, y int32) {
	var popRect win.RECT
	win.GetWindowRect(popupHWND, &popRect)
	dlgW := int(popRect.Right - popRect.Left)
	dlgH := int(popRect.Bottom - popRect.Top)

	usableW, usableH, startX, startY := sys.GetDisplayWorkArea()

	if targetHWND != 0 && win.IsWindowVisible(targetHWND) && !win.IsIconic(targetHWND) {
		var targetRect win.RECT
		win.GetWindowRect(targetHWND, &targetRect)

		centerCX := int(targetRect.Left + (targetRect.Right-targetRect.Left)/2)
		centerCY := int(targetRect.Top + (targetRect.Bottom-targetRect.Top)/2)

		xPos := clampScreenPos(centerCX-dlgW/2, startX, startX+usableW-dlgW)
		yPos := clampScreenPos(centerCY-dlgH/2, startY, startY+usableH-dlgH)
		return int32(xPos), int32(yPos)
	}

	xPos := clampScreenPos(startX+(usableW-dlgW)/2, startX, startX+usableW-dlgW)
	yPos := clampScreenPos(startY+(usableH-dlgH)/2, startY, startY+usableH-dlgH)
	return int32(xPos), int32(yPos)
}

func centerWindow(w *walk.MainWindow) {
	if w == nil || w.Handle() == 0 {
		return
	}

	hwnd := w.Handle()
	var rect win.RECT
	if !win.GetWindowRect(hwnd, &rect) {
		return
	}

	winW := int(rect.Right - rect.Left)
	winH := int(rect.Bottom - rect.Top)
	usableW, usableH, startX, startY := sys.GetDisplayWorkArea()

	flags := uint32(win.SWP_NOZORDER | win.SWP_NOACTIVATE | win.SWP_NOSIZE)
	if usableW > 0 && winW > usableW {
		winW = usableW
		flags &^= win.SWP_NOSIZE
	}
	if usableH > 0 && winH > usableH {
		winH = usableH
		flags &^= win.SWP_NOSIZE
	}

	x := clampScreenPos(startX+(usableW-winW)/2, startX, startX+usableW-winW)
	y := clampScreenPos(startY+(usableH-winH)/2, startY, startY+usableH-winH)

	win.SetWindowPos(hwnd, 0, int32(x), int32(y), int32(winW), int32(winH), flags)
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

func restoreFocus(parent walk.Form, hActive win.HWND) {
	if parent != nil && parent.Visible() && !win.IsIconic(parent.Handle()) {
		win.SetForegroundWindow(parent.Handle())
		win.SetFocus(parent.Handle())
	} else if hActive != 0 && win.IsWindowVisible(hActive) && !win.IsIconic(hActive) {
		win.SetForegroundWindow(hActive)
		win.SetFocus(hActive)
	}
}

// Idempotent action helpers that avoid redundant Win32 message broadcasts.
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