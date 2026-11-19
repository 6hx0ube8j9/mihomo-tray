package ui

import (
	"github.com/tailscale/walk"
	"github.com/tailscale/win"

	"mihomo-tray/internal/sys"
)

func ActivateWindow(hwnd win.HWND) {
	sys.ActivateWindow(uintptr(hwnd))
}

func centerWindow(w *walk.MainWindow) {
	if w == nil {
		return
	}
	sys.CenterWindow(uintptr(w.Handle()))
}

func centerDialog(dlg *walk.Dialog, owner walk.Form) {
	if dlg == nil {
		return
	}
	var ownerHwnd uintptr
	if owner != nil && owner.Visible() {
		ownerHwnd = uintptr(owner.Handle())
	}
	sys.CenterWindowOnOwner(uintptr(dlg.Handle()), ownerHwnd)
}

func restoreFocus(parent walk.Form, hActive win.HWND) {
	if parent != nil && parent.Visible() {
		sys.ActivateWindow(uintptr(parent.Handle()))
		return
	}
	sys.ActivateWindow(uintptr(hActive))
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