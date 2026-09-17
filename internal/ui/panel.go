package ui

import (
	"fmt"
	"log/slog"

	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"
)

type ProfileModel struct {
	walk.TableModelBase
	Items []ProfileItem
}

func (m *ProfileModel) RowCount() int {
	return len(m.Items)
}

func (m *ProfileModel) Value(row, col int) interface{} {
	item := m.Items[row]
	switch col {
	case 0:
		if item.IsActive {
			return "[使用中]"
		}
		return ""
	case 1:
		return item.Name
	case 2:
		if item.IsRemote {
			return "(远程订阅)"
		}
		return "(本地)"
	case 3:
		if !item.IsRemote {
			return "-"
		}
		if item.Interval > 0 {
			return fmt.Sprintf("%d 天", item.Interval)
		}
		return "禁用自动更新"
	case 4:
		if !item.IsRemote {
			return "-"
		}
		return item.LastUpdate
	}
	return ""
}

func (e *UIEngine) ShowProfileManager(items []ProfileItem) {
	e.app.Synchronize(func() {
		if e.panelWindow == nil {
			e.panelModel = &ProfileModel{Items: items}

			var actionSwitch *walk.Action
			var actionEditText *walk.Action
			var actionEditSub *walk.Action
			var actionUpdate *walk.Action
			var actionDelete *walk.Action

			err := MainWindow{
				AssignTo: &e.panelWindow,
				Title:    "配置面板",
				MinSize:  Size{Width: 600, Height: 300},
				Size:     Size{Width: 640, Height: 350},
				Layout:   VBox{},
				Children: []Widget{
					Composite{
						Layout: HBox{MarginsZero: true},
						Children: []Widget{
							PushButton{
								Text: "➕ 添加远程订阅",
								OnClicked: func() {
									e.sendCommand("RequestAddRemoteProfile", "")
									e.panelWindow.Hide()
								},
							},
							PushButton{
								Text: "📂 导入本地配置",
								OnClicked: func() {
									e.sendCommand("RequestAddLocalProfile", "")
									e.panelWindow.Hide()
								},
							},
							HSpacer{},
							PushButton{
								Text: "❌ 关闭面板",
								OnClicked: func() {
									e.panelWindow.Hide()
								},
							},
						},
					},
					TableView{
						AssignTo: &e.tableView,
						Columns: []TableViewColumn{
							{Title: "状态", Width: 65},
							{Title: "名称", Width: 180},
							{Title: "类型", Width: 80},
							{Title: "更新频率", Width: 100},
							{Title: "上次更新", Width: 120},
						},
						Model: e.panelModel,

						OnCurrentIndexChanged: func() {
							if e.tableView == nil || actionSwitch == nil {
								return
							}
							idx := e.tableView.CurrentIndex()
							if idx < 0 || idx >= len(e.panelModel.Items) {
								actionSwitch.SetEnabled(false)
								actionEditText.SetEnabled(false)
								actionEditSub.SetEnabled(false)
								actionUpdate.SetEnabled(false)
								actionDelete.SetEnabled(false)
								return
							}

							item := e.panelModel.Items[idx]
							actionSwitch.SetEnabled(!item.IsActive)
							actionDelete.SetEnabled(!item.IsActive)
							actionEditText.SetEnabled(true)
							actionEditSub.SetEnabled(item.IsRemote)
							actionUpdate.SetEnabled(item.IsRemote)
						},

						ContextMenuItems: []MenuItem{
							Action{
								AssignTo: &actionSwitch,
								Text:     "✔️ 切换配置",
								OnTriggered: func() {
									if idx := e.tableView.CurrentIndex(); idx >= 0 {
										e.sendCommand("SwitchProfile", e.panelModel.Items[idx].Path)
										e.panelWindow.Hide()
									}
								},
							},
							Action{
								AssignTo: &actionEditText,
								Text:     "📝 编辑文本",
								OnTriggered: func() {
									if idx := e.tableView.CurrentIndex(); idx >= 0 {
										e.sendCommand("OpenConfigFile", e.panelModel.Items[idx].Path)
									}
								},
							},
							Action{
								AssignTo: &actionEditSub,
								Text:     "⚙️ 编辑订阅",
								OnTriggered: func() {
									if idx := e.tableView.CurrentIndex(); idx >= 0 {
										e.sendCommand("RequestEditRemoteProfile", e.panelModel.Items[idx].Path)
										e.panelWindow.Hide()
									}
								},
							},
							Action{
								AssignTo: &actionUpdate,
								Text:     "🔄 立即更新",
								OnTriggered: func() {
									if idx := e.tableView.CurrentIndex(); idx >= 0 {
										e.sendCommand("UpdateRemoteProfile", e.panelModel.Items[idx].Path)
									}
								},
							},
							Separator{},
							Action{
								AssignTo: &actionDelete,
								Text:     "❌ 删除配置",
								OnTriggered: func() {
									if idx := e.tableView.CurrentIndex(); idx >= 0 {
										e.sendCommand("RemoveProfile", e.panelModel.Items[idx].Path)
										e.panelWindow.Hide()
									}
								},
							},
						},
					},
				},
			}.Create()

			if err != nil {
				slog.Error("创建配置面板主窗口失败", "err", err)
				return
			}

			e.panelWindow.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
				*canceled = true
				e.panelWindow.Hide()
			})
		}

		e.panelModel.Items = items
		e.panelModel.PublishRowsReset()
		if e.tableView != nil {
			e.tableView.SetCurrentIndex(-1)
		}

		if !e.panelWindow.Visible() {
			e.panelWindow.Show()
		}
		e.panelWindow.SetFocus()
	})
}
