package ui

import (
	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"
	"mihomo-tray/internal/domain"
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
	} else if !d.window.Visible() {
		d.ProfileView.RefreshData(d.lastState)
	}

	if !d.window.Visible() {
		d.window.Show()
	}
	ActivateWindow(d.window.Handle())
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
		slog.Error("配置管理窗口创建失败", "err", err)
		return
	}

	disableGhostToolbar(d.window)
	d.closePatch = ApplyHideOnClosePatch(d.window)
	centerWindow(d.window)

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
