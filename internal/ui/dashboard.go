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

func (m *ProfileModel) RowCount() int {
	return len(m.Items)
}

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

	var actionSwitch, actionEditText, actionEditSub, actionUpdate *walk.Action
	var actionMoveUp, actionMoveDown, actionDelete *walk.Action
	var btnMoveUp, btnMoveDown *walk.PushButton
	var btnAddRemote, btnAddLocal *walk.PushButton
	var statusLabel *walk.Label

	updateActionState := func() {
		if e.tableView == nil || actionSwitch == nil {
			return
		}
		idx := e.tableView.CurrentIndex()
		hasSelection := idx >= 0 && idx < len(e.panelModel.Items)

		if !hasSelection {
			actionSwitch.SetEnabled(false)
			actionEditText.SetEnabled(false)
			actionEditSub.SetEnabled(false)
			actionUpdate.SetEnabled(false)
			actionMoveUp.SetEnabled(false)
			actionMoveDown.SetEnabled(false)
			actionDelete.SetEnabled(false)
			if btnMoveUp != nil {
				btnMoveUp.SetEnabled(false)
			}
			if btnMoveDown != nil {
				btnMoveDown.SetEnabled(false)
			}
			return
		}

		item := e.panelModel.Items[idx]
		canMoveUp := idx > 0
		canMoveDown := idx < len(e.panelModel.Items)-1

		actionSwitch.SetEnabled(!item.IsActive)
		actionDelete.SetEnabled(!item.IsActive)
		actionEditText.SetEnabled(true)
		actionEditSub.SetEnabled(item.IsRemote)
		actionUpdate.SetEnabled(item.IsRemote)
		actionMoveUp.SetEnabled(canMoveUp)
		actionMoveDown.SetEnabled(canMoveDown)
		if btnMoveUp != nil {
			btnMoveUp.SetEnabled(canMoveUp)
		}
		if btnMoveDown != nil {
			btnMoveDown.SetEnabled(canMoveDown)
		}
	}

	err := MainWindow{
		AssignTo: &e.dashboardWindow,
		Title:    "Mihomo Tray 仪表盘",
		MinSize:  Size{Width: 700, Height: 350},
		Size:     Size{Width: 750, Height: 400},
		Font:     Font{Family: "Microsoft YaHei", PointSize: 10},
		// 1. 增加窗口整体的 Top 留白到 20px
		Layout:   VBox{Margins: Margins{Left: 15, Top: 20, Right: 15, Bottom: 15}, Spacing: 10},
		Children: []Widget{
			Composite{
				// 2. 使用 AlignHNearVCenter 实现垂直居中，Top 留白 12px
				Layout: HBox{
					Margins:   Margins{Left: 0, Top: 12, Right: 0, Bottom: 8},
					Spacing:   10,
					Alignment: AlignHNearVCenter,
				},
				// 3. 限制 Composite 容器最小高度为 42px，防止视口裁剪
				MinSize: Size{Height: 42},
				Children: []Widget{
					PushButton{
						AssignTo:  &btnAddRemote,
						Text:      "➕ 添加远程订阅",
						MinSize:   Size{Width: 130, Height: 32},
						OnClicked: func() { e.sendCommand(domain.ActionRequestAddRemote, "") },
					},
					PushButton{
						AssignTo:  &btnAddLocal,
						Text:      "📂 导入本地配置",
						MinSize:   Size{Width: 130, Height: 32},
						OnClicked: func() { e.sendCommand(domain.ActionRequestAddLocal, "") },
					},
					HSpacer{},
					Label{
						AssignTo: &statusLabel,
						Text:     "",
					},
				},
			},
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
						Model:                 e.panelModel,
						OnCurrentIndexChanged: updateActionState,
						ContextMenuItems: []MenuItem{
							Action{AssignTo: &actionSwitch, Text: "✔️ 切换配置", OnTriggered: func() {
								if idx := e.tableView.CurrentIndex(); idx >= 0 {
									e.sendCommand(domain.ActionSwitchProfile, e.panelModel.Items[idx].Path)
								}
							}},
							Action{AssignTo: &actionEditText, Text: "📝 打开文本", OnTriggered: func() {
								if idx := e.tableView.CurrentIndex(); idx >= 0 {
									e.sendCommand(domain.ActionOpenConfigFile, e.panelModel.Items[idx].Path)
								}
							}},
							Action{AssignTo: &actionEditSub, Text: "⚙️ 编辑订阅", OnTriggered: func() {
								if idx := e.tableView.CurrentIndex(); idx >= 0 {
									e.sendCommand(domain.ActionRequestEditRemote, e.panelModel.Items[idx].Path)
								}
							}},
							Action{AssignTo: &actionUpdate, Text: "🔄 立即更新", OnTriggered: func() {
								if idx := e.tableView.CurrentIndex(); idx >= 0 {
									e.sendCommand(domain.ActionUpdateRemoteProfile, e.panelModel.Items[idx].Path)
								}
							}},
							Separator{},
							Action{AssignTo: &actionMoveUp, Text: "⬆️ 向上移动", OnTriggered: func() {
								if idx := e.tableView.CurrentIndex(); idx >= 0 {
									e.sendCommand(domain.ActionMoveProfileUp, e.panelModel.Items[idx].Path)
								}
							}},
							Action{AssignTo: &actionMoveDown, Text: "⬇️ 向下移动", OnTriggered: func() {
								if idx := e.tableView.CurrentIndex(); idx >= 0 {
									e.sendCommand(domain.ActionMoveProfileDown, e.panelModel.Items[idx].Path)
								}
							}},
							Separator{},
							Action{AssignTo: &actionDelete, Text: "❌ 删除配置", OnTriggered: func() {
								if idx := e.tableView.CurrentIndex(); idx >= 0 {
									e.sendCommand(domain.ActionRemoveProfile, e.panelModel.Items[idx].Path)
								}
							}},
						},
					},
					Composite{
						Layout: VBox{MarginsZero: true, Spacing: 8},
						Children: []Widget{
							PushButton{AssignTo: &btnMoveUp, Text: "⬆️ 上移", Enabled: false, MinSize: Size{Width: 90, Height: 32}, OnClicked: func() {
								if idx := e.tableView.CurrentIndex(); idx >= 0 {
									e.sendCommand(domain.ActionMoveProfileUp, e.panelModel.Items[idx].Path)
								}
							}},
							PushButton{AssignTo: &btnMoveDown, Text: "⬇️ 下移", Enabled: false, MinSize: Size{Width: 90, Height: 32}, OnClicked: func() {
								if idx := e.tableView.CurrentIndex(); idx >= 0 {
									e.sendCommand(domain.ActionMoveProfileDown, e.panelModel.Items[idx].Path)
								}
							}},
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

	updateActionState()
	e.mw = e.dashboardWindow

	return nil
}

func (e *UIEngine) showDashboard() {
	if e.dashboardWindow == nil {
		return
	}
	e.dashboardWindow.Show()
}

func (e *UIEngine) ShowProfileManager(items []domain.UIProfileItem) {
	if e.app == nil {
		return
	}
	e.app.Synchronize(func() {
		e.lastProfileItems = items
		if e.panelModel != nil {
			e.panelModel.Items = items
			e.panelModel.PublishRowsReset()
		}
		e.showDashboard()
	})
}

func (e *UIEngine) RefreshPanelData(items []domain.UIProfileItem) {
	if e.app == nil {
		return
	}
	e.app.Synchronize(func() {
		e.lastProfileItems = items
		if e.dashboardWindow == nil || !e.dashboardWindow.Visible() {
			return
		}

		var selectedPath string
		if e.tableView != nil {
			idx := e.tableView.CurrentIndex()
			if idx >= 0 && idx < len(e.panelModel.Items) {
				selectedPath = e.panelModel.Items[idx].Path
			}
		}

		e.panelModel.Items = items
		e.panelModel.PublishRowsReset()

		if e.tableView != nil && selectedPath != "" {
			newIdx := -1
			for i, item := range items {
				if item.Path == selectedPath {
					newIdx = i
					break
				}
			}
			if newIdx >= 0 {
				e.tableView.SetCurrentIndex(newIdx)
			}
			e.tableView.Invalidate()
		}
	})
}
