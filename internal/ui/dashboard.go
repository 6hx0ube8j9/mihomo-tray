package ui

import (
	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"

	"mihomo-tray/internal/domain"
)

type ProfileModel struct {
	walk.TableModelBase
	Items []domain.UIProfileItem
}

func (m *ProfileModel) RowCount() int { return len(m.Items) }
func (m *ProfileModel) Value(row, col int) interface{} { return "" }

func (e *UIEngine) InitDashboardWindow() error {
	e.panelModel = &ProfileModel{}

	var btnAddRemote, btnAddLocal *walk.PushButton
	var statusLabel *walk.Label

	// 【测试 2 段】：恢复顶级字体设置 + 顶部 Composite 按钮栏
	err := MainWindow{
		AssignTo: &e.dashboardWindow,
		Title:    "排查测试 - 阶段 1：顶部按钮栏",
		MinSize:  Size{Width: 700, Height: 350},
		Size:     Size{Width: 750, Height: 400},
		Font:     Font{Family: "Microsoft YaHei", PointSize: 10},
		Layout:   VBox{Margins: Margins{Left: 15, Top: 15, Right: 15, Bottom: 15}, Spacing: 10},
		Children: []Widget{
			Composite{
				Layout: HBox{Margins: Margins{Left: 0, Top: 5, Right: 0, Bottom: 5}, Spacing: 10},
				Children: []Widget{
					PushButton{
						AssignTo:  &btnAddRemote,
						Text:      "➕ 添加远程订阅",
						OnClicked: func() {},
					},
					PushButton{
						AssignTo:  &btnAddLocal,
						Text:      "📂 导入本地配置",
						OnClicked: func() {},
					},
					HSpacer{},
					Label{
						AssignTo: &statusLabel,
						Text:     "状态栏测试",
					},
				},
			},
			PushButton{
				Text: "占位按钮（观察上方按钮是否挤压/截断）",
			},
		},
	}.Create()

	if err != nil {
		return err
	}

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

func (e *UIEngine) ShowProfileManager(items []domain.UIProfileItem) { e.showDashboard() }
func (e *UIEngine) RefreshPanelData(items []domain.UIProfileItem) {}
