package ui

import (
	"fmt"
	"path/filepath"

	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"
	"mihomo-tray/internal/domain"
)

type ProfileView struct {
	engine     *Engine
	tableView  *walk.TableView
	model      *ProfileModel
	portsLabel *walk.Label
}

func NewProfileView(e *Engine) *ProfileView {
	return &ProfileView{
		engine: e,
		model:  &ProfileModel{Items: []domain.UIProfileItem{}},
	}
}

func (v *ProfileView) Declarative() []Widget {
	var actionSwitch, actionEditText, actionEditInfo, actionUpdate *walk.Action
	var actionMoveUp, actionMoveDown, actionDelete *walk.Action
	var btnMoveUp, btnMoveDown *walk.PushButton

	updateActionState := func() {
		if v.tableView == nil || actionSwitch == nil {
			return
		}
		idx := v.tableView.CurrentIndex()
		hasSelection := idx >= 0 && idx < len(v.model.Items)

		if !hasSelection {
			actionSwitch.SetEnabled(false); actionEditText.SetEnabled(false); actionEditInfo.SetEnabled(false)
			actionUpdate.SetEnabled(false); actionMoveUp.SetEnabled(false); actionMoveDown.SetEnabled(false)
			actionDelete.SetEnabled(false)
			if btnMoveUp != nil { btnMoveUp.SetEnabled(false) }
			if btnMoveDown != nil { btnMoveDown.SetEnabled(false) }
			return
		}

		item := v.model.Items[idx]
		canMoveUp, canMoveDown := idx > 0, idx < len(v.model.Items)-1

		actionSwitch.SetEnabled(!item.IsActive); actionDelete.SetEnabled(true)
		actionEditText.SetEnabled(true); actionEditInfo.SetEnabled(true); actionUpdate.SetEnabled(item.IsRemote)
		actionMoveUp.SetEnabled(canMoveUp); actionMoveDown.SetEnabled(canMoveDown)
		if btnMoveUp != nil { btnMoveUp.SetEnabled(canMoveUp) }
		if btnMoveDown != nil { btnMoveDown.SetEnabled(canMoveDown) }
	}

	return []Widget{
		Composite{
			Layout: HBox{MarginsZero: true, Spacing: 12},
			Children: []Widget{
				PushButton{Text: "添加远程订阅", OnClicked: func() { v.engine.SendCommand(domain.ActionRequestAddRemote, "") }},
				PushButton{Text: "导入本地配置", OnClicked: func() { v.engine.SendCommand(domain.ActionRequestAddLocal, "") }},
				PushButton{Text: "更改代理端口", OnClicked: func() { v.engine.SendCommand(domain.ActionRequestEditPort, "") }},
				PushButton{Text: "Web 面板设置", OnClicked: func() { v.engine.SendCommand(domain.ActionRequestEditController, "") }},
				
				HSpacer{}, 
				
				Label{
					AssignTo: &v.portsLabel,
					Font:     Font{Family: "JetBrains Mono"},
				},
			},
		},

		Composite{
			Layout: HBox{MarginsZero: true, Spacing: 10},
			Children: []Widget{
				TableView{
					AssignTo: &v.tableView,
					Columns: []TableViewColumn{
						{Title: "状态", Width: 80, Alignment: AlignCenter},
						{Title: "名称", Width: 180},
						{Title: "文件名", Width: 130},
						{Title: "类型", Width: 80, Alignment: AlignCenter},
						{Title: "更新频率", Width: 90, Alignment: AlignCenter},
						{Title: "上次更新", Width: 130, Alignment: AlignCenter},
					},
					Model:                 v.model,
					OnCurrentIndexChanged: updateActionState,
					OnItemActivated: func() {
						if idx := v.tableView.CurrentIndex(); idx >= 0 {
							v.engine.SendCommand(domain.ActionSwitchProfile, v.model.Items[idx].Path)
						}
					},
					ContextMenuItems: []MenuItem{
						Action{AssignTo: &actionSwitch, Text: "切换配置", OnTriggered: func() {
							if idx := v.tableView.CurrentIndex(); idx >= 0 { v.engine.SendCommand(domain.ActionSwitchProfile, v.model.Items[idx].Path) }
						}},
						Action{AssignTo: &actionEditText, Text: "打开文本", OnTriggered: func() {
							if idx := v.tableView.CurrentIndex(); idx >= 0 { v.engine.SendCommand(domain.ActionOpenConfigFile, v.model.Items[idx].Path) }
						}},
						Action{AssignTo: &actionEditInfo, Text: "编辑信息", OnTriggered: func() {
							if idx := v.tableView.CurrentIndex(); idx >= 0 { v.engine.SendCommand(domain.ActionEditProfileInfo, v.model.Items[idx].Path) }
						}},
						Action{AssignTo: &actionUpdate, Text: "立即更新", OnTriggered: func() {
							if idx := v.tableView.CurrentIndex(); idx >= 0 { v.engine.SendCommand(domain.ActionUpdateRemoteProfile, v.model.Items[idx].Path) }
						}},
						Separator{},
						Action{AssignTo: &actionMoveUp, Text: "向上移动", OnTriggered: func() {
							if idx := v.tableView.CurrentIndex(); idx >= 0 { v.engine.SendCommand(domain.ActionMoveProfileUp, v.model.Items[idx].Path) }
						}},
						Action{AssignTo: &actionMoveDown, Text: "向下移动", OnTriggered: func() {
							if idx := v.tableView.CurrentIndex(); idx >= 0 { v.engine.SendCommand(domain.ActionMoveProfileDown, v.model.Items[idx].Path) }
						}},
						Separator{},
						Action{AssignTo: &actionDelete, Text: "删除配置", OnTriggered: func() {
							if idx := v.tableView.CurrentIndex(); idx >= 0 { v.engine.SendCommand(domain.ActionRemoveProfile, v.model.Items[idx].Path) }
						}},
					},
				},

				Composite{
					Layout: VBox{MarginsZero: true, Spacing: 8},
					Children: []Widget{
						PushButton{AssignTo: &btnMoveUp, Text: "上移", Enabled: false, MinSize: Size{Width: 90}, OnClicked: func() {
							if idx := v.tableView.CurrentIndex(); idx >= 0 { v.engine.SendCommand(domain.ActionMoveProfileUp, v.model.Items[idx].Path) }
						}},
						PushButton{AssignTo: &btnMoveDown, Text: "下移", Enabled: false, MinSize: Size{Width: 90}, OnClicked: func() {
							if idx := v.tableView.CurrentIndex(); idx >= 0 { v.engine.SendCommand(domain.ActionMoveProfileDown, v.model.Items[idx].Path) }
						}},
						VSpacer{},
					},
				},
			},
		},
	}
}

func (v *ProfileView) RefreshData(state domain.UIState) {
	if v.portsLabel != nil {
		v.portsLabel.SetText(fmt.Sprintf("Mixed  : %d\nSocks  : %d\nHTTP(S): %d",
			state.MixedPort, state.SocksPort, state.HttpPort))
	}

	items := state.ProfileItems
	
	var selectedPath string
	if v.tableView != nil {
		if idx := v.tableView.CurrentIndex(); idx >= 0 && idx < len(v.model.Items) {
			selectedPath = v.model.Items[idx].Path
		}
	}

	v.model.Items = items
	v.model.PublishRowsReset()

	if v.tableView != nil && selectedPath != "" {
		newIdx := -1
		for i, item := range items {
			if item.Path == selectedPath {
				newIdx = i
				break
			}
		}
		if newIdx >= 0 {
			v.tableView.SetCurrentIndex(newIdx)
		}
		v.tableView.Invalidate()
	}
}

type ProfileModel struct {
	walk.TableModelBase
	Items []domain.UIProfileItem
}

func (m *ProfileModel) RowCount() int { return len(m.Items) }

func (m *ProfileModel) Value(row, col int) interface{} {
	item := m.Items[row]
	switch col {
	case 0:
		if item.IsActive { return "✔ 使用中" }
		return ""
	case 1:
		return item.Name
	case 2:
		return filepath.Base(item.Path)
	case 3:
		if item.IsRemote { return "订阅配置" }
		return "本地配置"
	case 4:
		if !item.IsRemote { return "-" }
		if item.Interval > 0 { return fmt.Sprintf("%d 天", item.Interval) }
		return "停止更新"
	case 5:
		if !item.IsRemote { return "-" }
		return item.LastUpdate
	}
	return ""
}
