package view

import (
	"fmt"

	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"

	"mihomo-tray/internal/tray"
)

type ProfileModel struct {
	walk.TableModelBase
	Items []tray.ProfileItem
}

func (m *ProfileModel) RowCount() int {
	return len(m.Items)
}

func (m *ProfileModel) Value(row, col int) interface{} {
	item := m.Items[row]

	switch col {
	case 0: // 状态
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

func RunProfileManager(items []tray.ProfileItem, dispatch func(action, payload string)) error {
	app, err := walk.InitApp()
	if err != nil {
		return fmt.Errorf("初始化 walk App 失败: %w", err)
	}

	var mw *walk.MainWindow
	var tv *walk.TableView

	var actionSwitch *walk.Action
	var actionEditText *walk.Action
	var actionEditSub *walk.Action
	var actionUpdate *walk.Action
	var actionDelete *walk.Action

	model := &ProfileModel{Items: items}

	err = MainWindow{
		AssignTo: &mw,
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
							dispatch("RequestAddRemoteProfile", "")
							mw.Close()
						},
					},
					PushButton{
						Text: "📂 导入本地配置",
						OnClicked: func() {
							dispatch("RequestAddLocalProfile", "")
							mw.Close()
						},
					},
					HSpacer{},
					PushButton{
						Text: "❌ 关闭面板",
						OnClicked: func() {
							mw.Close()
						},
					},
				},
			},
			TableView{
				AssignTo: &tv,
				Columns: []TableViewColumn{
					{Title: "状态", Width: 65},
					{Title: "名称", Width: 180},
					{Title: "类型", Width: 80},
					{Title: "更新频率", Width: 100},
					{Title: "上次更新", Width: 120},
				},
				Model: model,

				OnCurrentIndexChanged: func() {
					if tv == nil || actionSwitch == nil || actionDelete == nil || 
						actionEditText == nil || actionEditSub == nil || actionUpdate == nil {
						return
					}

					idx := tv.CurrentIndex()

					if idx < 0 || idx >= len(model.Items) {
						actionSwitch.SetEnabled(false)
						actionEditText.SetEnabled(false)
						actionEditSub.SetEnabled(false)
						actionUpdate.SetEnabled(false)
						actionDelete.SetEnabled(false)
						return
					}

					item := model.Items[idx]
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
							if idx := tv.CurrentIndex(); idx >= 0 {
								dispatch("SwitchProfile", model.Items[idx].Path)
								mw.Close()
							}
						},
					},
					Action{
						AssignTo: &actionEditText,
						Text:     "📝 编辑文本",
						OnTriggered: func() {
							if idx := tv.CurrentIndex(); idx >= 0 {
								dispatch("OpenConfigFile", model.Items[idx].Path)
							}
						},
					},
					Action{
						AssignTo: &actionEditSub,
						Text:     "⚙️ 编辑订阅",
						OnTriggered: func() {
							if idx := tv.CurrentIndex(); idx >= 0 {
								dispatch("RequestEditRemoteProfile", model.Items[idx].Path)
								mw.Close()
							}
						},
					},
					Action{
						AssignTo: &actionUpdate,
						Text:     "🔄 立即更新",
						OnTriggered: func() {
							if idx := tv.CurrentIndex(); idx >= 0 {
								dispatch("UpdateRemoteProfile", model.Items[idx].Path)
							}
						},
					},
					Separator{},
					Action{
						AssignTo: &actionDelete,
						Text:     "❌ 删除配置",
						OnTriggered: func() {
							if idx := tv.CurrentIndex(); idx >= 0 {
								dispatch("RemoveProfile", model.Items[idx].Path)
								mw.Close()
							}
						},
					},
				},
			},
		}, 
	}.Create()

	if err != nil {
		return err
	}

	tv.SetCurrentIndex(-1)

	mw.Show()
	app.Run()

	return nil
}
