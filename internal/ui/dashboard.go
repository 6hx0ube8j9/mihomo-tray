package ui

import (
	"log/slog"

	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"

	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/sys"
)

type Dashboard struct {
	engine      *Engine
	window      *walk.MainWindow
	ProfileView *ProfileView
	lastState   domain.UIState

	closePatch *WindowHidePatch
}

func NewDashboard(e *Engine) *Dashboard {
	return &Dashboard{
		engine:      e,
		ProfileView: NewProfileView(e),
	}
}

func (d *Dashboard) Show() {
	if d.window == nil {
		d.createWindow()
		if d.window == nil {
			return
		}
		d.window.Show()
		sys.ActivateWindow(uintptr(d.window.Handle()))
		return
	}

	hwnd := uintptr(d.window.Handle())
	if !d.window.Visible() {
		d.ProfileView.RefreshData(d.lastState)
		sys.EnsureWindowOnScreen(hwnd)
		d.window.Show()
		sys.ActivateWindow(hwnd)
		return
	}

	sys.ActivateWindow(hwnd)
	sys.EnsureWindowOnScreen(hwnd)
}

func (d *Dashboard) createWindow() {
	err := MainWindow{
		AssignTo: &d.window,
		Title:    "管理配置",
		Icon:     1,
		Visible:  false,
		MinSize:  Size{Width: 700, Height: 350},
		Size:     Size{Width: 780, Height: 450},
		Font:     Font{Family: "Microsoft YaHei", PointSize: 10},
		Layout:   VBox{Margins: Margins{Left: 15, Top: 15, Right: 15, Bottom: 15}, Spacing: 10},
		Children: d.ProfileView.Declarative(),
	}.Create()

	if err != nil {
		slog.Error("创建配置管理窗口失败", "err", err)
		return
	}

	disableGhostToolbar(d.window)
	d.closePatch = ApplyHideOnClosePatch(d.window)
	sys.CenterWindow(uintptr(d.window.Handle()))

	if len(d.lastState.ProfileItems) > 0 || d.lastState.MixedPort != 0 {
		d.ProfileView.RefreshData(d.lastState)
	}
}

func (d *Dashboard) UpdateState(state domain.UIState) {
	d.lastState = state
	if d.window == nil || !d.window.Visible() {
		return
	}
	d.ProfileView.RefreshData(state)
}

func (d *Dashboard) Dispose() {
	if d.window == nil {
		return
	}
	if d.closePatch != nil {
		d.closePatch.Remove(d.window)
		d.closePatch = nil
	}
	d.window.Dispose()
	d.window = nil
}
