package view

import (
	"fmt"

	"github.com/lxn/walk"
    . "github.com/lxn/walk/declarative"

	"mihomo-tray/internal/tray"
)

type ProfileModel struct {
	walk.TableModelBase
	Items []tray.ProfileItem
}

func NewProfileModel(items []tray.ProfileItem) *ProfileModel {
	return &ProfileModel{Items: items}
}

func (m *ProfileModel) RowCount() int {
	return len(m.Items)
}

func (m *ProfileModel) Value(row, col int) interface{} {
	item := m.Items[row]

	switch col {
	case 0: // 状态列
		if item.IsActive {
			return "[使用中]"
		}
		return ""
	case 1: // 名称列
		return item.Name
	case 2: // 类型列
		if item.IsRemote {
			return "(远程订阅)"
		}
		return "(本地)"
	case 3: // 更新频率列
		if !item.IsRemote {
			return "-"
		}
		if item.Interval > 0 {
			return fmt.Sprintf("%d 小时", item.Interval)
		}
		return "不自动"
	case 4: // 上次更新列
		if !item.IsRemote {
			return "-"
		}
		return item.LastUpdate
	}
	return ""
}

func RunProfileManager(items []tray.ProfileItem, dispatch func(action, payload string)) error {
	var mw *walk.MainWindow
	var tv *walk.TableView

	// 定义右键菜单的动作指针（为了后续实现自动置灰逻辑）
	var actionSwitch *walk.Action
	var actionEditText *walk.Action
	var actionEditSub *walk.Action
	var actionUpdate *walk.Action
	var actionDelete *walk.Action

	// 实例化双向数据绑定模型
	model := NewProfileModel(items)

	err := MainWindow{
		AssignTo: &mw,
		Title:    "配置面板",
		MinSize:  Size{Width: 600, Height: 350},
		Size:     Size{Width: 640, Height: 400},
		Layout:   VBox{}, // 垂直布局
		Children: []Widget{
			// --- 1. 顶部操作栏 ---
			Composite{
				Layout: HBox{MarginsZero: true},
				Children: []Widget{
					PushButton{
						Text: "➕ 添加远程订阅",
						OnClicked: func() {
							dispatch("RequestAddRemoteProfile", "")
						},
					},
					PushButton{
						Text: "📂 导入本地配置",
						OnClicked: func() {
							dispatch("RequestAddLocalProfile", "")
						},
					},
					HSpacer{}, // 弹簧占位符，把右边的按钮推过去
					PushButton{
						Text: "❌ 关闭面板",
						OnClicked: func() {
							mw.Close()
						},
					},
				},
			},

			// --- 2. 核心数据表格 (SysListView32) ---
			TableView{
				AssignTo: &tv,
				Columns: []TableViewColumn{
					{Title: "状态", Width: 65},
					{Title: "名称", Width: 180},
					{Title: "类型", Width: 80},
					{Title: "更新频率", Width: 80},
					{Title: "上次更新", Width: 120},
				},
				Model: model, // 完美绑定底层数据

				OnCurrentIndexChanged: func() {
					idx := tv.CurrentIndex()
					if idx < 0 || idx >= len(model.Items) {
						return
					}
					item := model.Items[idx]

					actionSwitch.SetEnabled(!item.IsActive)
					actionDelete.SetEnabled(!item.IsActive)

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
								mw.Close() // 切换成功后自动关掉面板，体验极佳
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

	mw.Run()
	return nil
}
