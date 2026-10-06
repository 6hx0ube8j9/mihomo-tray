package ui

import (
	"syscall"

	"github.com/tailscale/walk"
	"github.com/tailscale/win"
)

// Workaround for tailscale/walk bug (Commit 3490772, 2024-12-03). 
// Upstream forces WS_VISIBLE on the default toolbar, currently known to only affect MainWindow.
// This empty toolbar overlaps top UI elements. Manually hiding it restores the correct layout.
func disableGhostToolbar(win *walk.MainWindow) {
	if win == nil {
		return
	}
	if tb := win.ToolBar(); tb != nil {
		tb.SetVisible(false)
		tb.Dispose()
	}
}

func centerWindow(w *walk.MainWindow) {
	if w == nil {
		return
	}
	monitor := walk.PrimaryMonitor()
	workArea := monitor.WorkArea()
	bounds := w.Bounds()
	newX, newY := workArea.X+(workArea.Width-bounds.Width)/2, workArea.Y+(workArea.Height-bounds.Height)/2
	if newX < 0 {
		newX = 0
	}
	if newY < 0 {
		newY = 0
	}
	w.SetBounds(walk.Rectangle{X: newX, Y: newY, Width: bounds.Width, Height: bounds.Height})
}

// ---------------- Window Message Hooks ----------------

// WindowHidePatch holds the WndProc hook state for safe cleanup.
type WindowHidePatch struct {
	wndProcCb  uintptr
	oldWndProc uintptr
}

// ApplyHideOnClosePatch intercepts WM_CLOSE to hide the window instead of exiting.
func ApplyHideOnClosePatch(mw *walk.MainWindow) *WindowHidePatch {
	if mw == nil {
		return nil
	}
	patch := &WindowHidePatch{}
	hwnd := mw.Handle()

	patch.wndProcCb = syscall.NewCallback(func(h win.HWND, msg uint32, wParam, lParam uintptr) uintptr {
		if msg == win.WM_CLOSE {
			win.ShowWindow(h, win.SW_HIDE)
			return 0
		}

		oldProc := patch.oldWndProc
		if msg == win.WM_NCDESTROY {
			if patch.oldWndProc != 0 {
				win.SetWindowLongPtr(h, win.GWLP_WNDPROC, patch.oldWndProc)
				patch.oldWndProc = 0
			}
		}

		return win.CallWindowProc(oldProc, h, msg, wParam, lParam)
	})

	patch.oldWndProc = win.SetWindowLongPtr(hwnd, win.GWLP_WNDPROC, patch.wndProcCb)
	return patch
}

func (p *WindowHidePatch) Remove(mw *walk.MainWindow) {
	if p == nil || mw == nil {
		return
	}
	hwnd := mw.Handle()
	if hwnd != 0 && p.oldWndProc != 0 {
		win.SetWindowLongPtr(hwnd, win.GWLP_WNDPROC, p.oldWndProc)
		p.oldWndProc = 0
	}
}
