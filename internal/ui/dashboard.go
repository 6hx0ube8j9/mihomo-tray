package ui

import (
	"syscall"

	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"
	"github.com/tailscale/win"
	"mihomo-tray/internal/domain"
)

type Dashboard struct {
	engine      *Engine
	window      *walk.MainWindow
	ProfileView *ProfileView
	lastState   domain.UIState 
}

func NewDashboard(e *Engine) *Dashboard {
	return &Dashboard{
		engine:      e,
		ProfileView: NewProfileView(e),
	}
}

func (d *Dashboard) Show() {
	d.engine.app.Synchronize(func() {
		if d.window == nil {
			d.createWindow()
		}

		hwnd := d.window.Handle()
		if win.IsIconic(hwnd) {
			win.ShowWindow(hwnd, win.SW_RESTORE)
		}
		if !d.window.Visible() {
			d.window.Show()
		}
		win.SetForegroundWindow(hwnd)
		d.window.SetFocus()
	})
}

func (d *Dashboard) createWindow() {
	err := MainWindow{
		AssignTo: &d.window,
		Title:    "管理配置",
		MinSize:  Size{Width: 700, Height: 350},
		Size:     Size{Width: 780, Height: 450},
		Font:     Font{Family: "Microsoft YaHei", PointSize: 10},
		Layout:   VBox{Margins: Margins{Left: 15, Top: 15, Right: 15, Bottom: 15}, Spacing: 10},
		Children: d.ProfileView.Declarative(),
	}.Create()

	if err != nil {
		return
	}

    // Apply upstream layout patch.
	disableGhostToolbar(d.window)

	var oldWndProc uintptr
	newWndProc := syscall.NewCallback(func(hwnd win.HWND, msg uint32, wParam, lParam uintptr) uintptr {
		if msg == win.WM_CLOSE {
			win.ShowWindow(hwnd, win.SW_HIDE)
			return 0
		}
		return win.CallWindowProc(oldWndProc, hwnd, msg, wParam, lParam)
	})
	oldWndProc = win.SetWindowLongPtr(d.window.Handle(), win.GWLP_WNDPROC, newWndProc)

	centerWindow(d.window)

	if len(d.lastState.ProfileItems) > 0 || d.lastState.MixedPort != 0 {
		d.ProfileView.RefreshData(d.lastState)
	}
}

func (d *Dashboard) Refresh(state domain.UIState) {
	d.lastState = state
	if d.window == nil || !d.window.Visible() {
		return
	}
	d.ProfileView.RefreshData(state)
}

func (d *Dashboard) RefreshData(state domain.UIState) {
	d.lastState = state
	d.ProfileView.RefreshData(state) 
}

func (d *Dashboard) Dispose() {
	if d.window != nil {
		d.window.Dispose()
	}
}

// Workaround for tailscale/walk bug (Commit 3490772, 2024-12-03). 
// Upstream forces WS_VISIBLE on the default toolbar, currently known to only affect MainWindow.
// This empty toolbar overlaps top UI elements. Manually hiding it restores the correct layout.
func disableGhostToolbar(win *walk.MainWindow) {
	if win != nil {
		if tb := win.ToolBar(); tb != nil {
			tb.SetVisible(false)
		}
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
