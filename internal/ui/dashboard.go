package ui

import (
	"fmt"

	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"

	"mihomo-tray/internal/domain"
)

type ProfileModel struct {
	walk.TableModelBase
	Items []domain.UIProfileItem
}

func (m *ProfileModel) RowCount() int { return len(m.Items) }
func (m *ProfileModel) Value(row, col int) interface{} {
	if row < 0 || row >= len(m.Items) {
		return ""
	}
	item := m.Items[row]
	switch col {
	case 0:
		if item.IsActive {
			return "使用中"
		}
		return ""
	case 1:
		return item.Name
	case 2:
		if item.IsRemote {
			return "订阅配置"
		}
		return "本地配置"
	case 3:
		if !item.IsRemote {
			return "-"
		}
		if item.Interval > 0 {
			return fmt.Sprintf("%d 天", item.Interval)
		}
		return "停止更新"
	case 4:
		if !item.IsRemote {
			return "-"
		}
		return item.LastUpdate
	}
	return ""
}

func (e *UIEngine) InitDashboardWindow() error {
	e.panelModel = &ProfileModel{
		Items: []domain.UIProfileItem{
			{IsActive: true, Name: "示例节点订阅 - 香港", IsRemote: true, Interval: 1, LastUpdate: "2026-03-30 10:00", Path: "sub1"},
			{IsActive: false, Name: "本地自建备用节点", IsRemote: false, Interval: 0, LastUpdate: "-", Path: "local1"},
			{IsActive: false, Name: "团队公共订阅 - 日本", IsRemote: true, Interval: 7, LastUpdate: "2026-03-28 14:20", Path: "sub2"},
		},
	}

	var btnAddRemote, btnAddLocal *walk.PushButton
	var btnMoveUp, btnMoveDown *walk.PushButton
	var statusLabel *walk.Label

	err := MainWindow{
		AssignTo: &e.dashboardWindow,
		Title:    "Mihomo Tray 仪表盘",
		MinSize:  Size{Width: 700, Height: 350},
		Size:     Size{Width: 750, Height: 400},
		Font:     Font{Family: "Microsoft YaHei", PointSize: 10},
		Layout:   VBox{Margins: Margins{Left: 15, Top: 15, Right: 15, Bottom: 15}, Spacing: 12},
		Children: []Widget{
			// 修复 1：MarginsZero 消除边距冲突，锁定 Composite 最小高度为 32px
			Composite{
				Layout:  HBox{MarginsZero: true, Spacing: 10},
				MinSize: Size{Height: 32},
				Children: []Widget{
					PushButton{
						AssignTo:  &btnAddRemote,
						Text:      "➕ 添加远程订阅",
						MinSize:   Size{Width: 130, Height: 30}, // 修复 2：给按钮赋予固定的安全高度
						OnClicked: func() {},
					},
					PushButton{
						AssignTo:  &btnAddLocal,
						Text:      "📂 导入本地配置",
						MinSize:   Size{Width: 130, Height: 30},
						OnClicked: func() {},
					},
					HSpacer{},
					Label{
						AssignTo: &statusLabel,
						Text:     "",
					},
				},
			},
			// 主体区域：表格 + 右侧按钮
			Composite{
				Layout: HBox{MarginsZero: true, Spacing: 10},
				Children: []Widget{
					TableView{
						AssignTo: &e.tableView,
						Columns: []TableViewColumn{
							{Title: "状态", Width: 90},
							{Title: "名称", Width: 220},
							{Title: "类型", Width: 80},
							{Title: "更新频率", Width: 100},
							{Title: "上次更新", Width: 130},
						},
						Model: e.panelModel,
					},
					Composite{
						Layout: VBox{MarginsZero: true, Spacing: 8},
						Children: []Widget{
							PushButton{AssignTo: &btnMoveUp, Text: "⬆️ 上移", Enabled: false, MinSize: Size{Width: 90, Height: 30}},
							PushButton{AssignTo: &btnMoveDown, Text: "⬇️ 下移", Enabled: false, MinSize: Size{Width: 90, Height: 30}},
							VSpacer{},
						},
					},
				},
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
