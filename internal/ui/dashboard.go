package ui

import (
	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"

	"mihomo-tray/internal/domain"
)

func (e *UIEngine) InitDashboardWindow() error {
	// 【测试 1 段】：最简窗口骨架，测试窗口顶栏与最上方控件是否截断
	err := MainWindow{
		AssignTo: &e.dashboardWindow,
		Title:    "排查测试 - 最小骨架",
		MinSize:  Size{Width: 700, Height: 350},
		Size:     Size{Width: 750, Height: 400},
		Layout:   VBox{Margins: Margins{Left: 15, Top: 15, Right: 15, Bottom: 15}, Spacing: 10},
		Children: []Widget{
			PushButton{
				Text: "测试按钮 1（观察顶部与左右边距是否正常）",
			},
			PushButton{
				Text: "测试按钮 2",
			},
		},
	}.Create()

	if err != nil {
		return err
	}

	// 拦截关闭按钮，仅作隐藏
	e.dashboardWindow.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		*canceled = true
		e.dashboardWindow.SetVisible(false)
	})

	e.mw = e.dashboardWindow
	return nil
}

func (e *UIEngine) showDashboard() {
	if e.dashboardWindow != nil {
		e.dashboardWindow.Show()
	}
}

func (e *UIEngine) ShowProfileManager(items []domain.UIProfileItem) {
	e.showDashboard()
}

func (e *UIEngine) RefreshPanelData(items []domain.UIProfileItem) {
	// 占位防报错
}
