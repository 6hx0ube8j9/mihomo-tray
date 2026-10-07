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

	sendWithSelected := func(action string) {
		if v.tableView == nil {
			return
		}
		idx := v.tableView.CurrentIndex()
		if idx >= 0 && idx < len(v.model.Items) {
			v.engine.SendCommand(action, v.model.Items[idx].Path)
		}
	}

	updateActionState := func() {
		if v.tableView == nil || actionSwitch == nil {
			return
		}
		idx := v.tableView.CurrentIndex()
		hasSelection := idx >= 0 && idx < len(v.model.Items)

		var canSwitch, canUpdate, canMoveUp, canMoveDown bool
		if hasSelection {
			item := v.model.Items[idx]
			canSwitch = !item.IsActive
			canUpdate = item.IsRemote
			canMoveUp = idx > 0
			canMoveDown = idx < len(v.model.Items)-1
		}

		actionSwitch.SetEnabled(canSwitch)
		actionEditText.SetEnabled(hasSelection)
		actionEditInfo.SetEnabled(hasSelection)
		actionUpdate.SetEnabled(canUpdate)
		actionDelete.SetEnabled(hasSelection)

		actionMoveUp.SetEnabled(canMoveUp)
		actionMoveDown.SetEnabled(canMoveDown)
		if btnMoveUp != nil {
			btnMoveUp.SetEnabled(canMoveUp)
		}
		if btnMoveDown != nil {
			btnMoveDown.SetEnabled(canMoveDown)
		}
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
					OnItemActivated:       func() { sendWithSelected(domain.ActionSwitchProfile) },
					ContextMenuItems: []MenuItem{
						Action{AssignTo: &actionSwitch, Text: "切换配置", OnTriggered: func() { sendWithSelected(domain.ActionSwitchProfile) }},
						Action{AssignTo: &actionEditText, Text: "打开文本", OnTriggered: func() { sendWithSelected(domain.ActionOpenConfigFile) }},
						Action{AssignTo: &actionEditInfo, Text: "编辑信息", OnTriggered: func() { sendWithSelected(domain.ActionEditProfileInfo) }},
						Action{AssignTo: &actionUpdate, Text: "立即更新", OnTriggered: func() { sendWithSelected(domain.ActionUpdateRemoteProfile) }},
						Separator{},
						Action{AssignTo: &actionMoveUp, Text: "向上移动", OnTriggered: func() { sendWithSelected(domain.ActionMoveProfileUp) }},
						Action{AssignTo: &actionMoveDown, Text: "向下移动", OnTriggered: func() { sendWithSelected(domain.ActionMoveProfileDown) }},
						Separator{},
						Action{AssignTo: &actionDelete, Text: "删除配置", OnTriggered: func() { sendWithSelected(domain.ActionRemoveProfile) }},
					},
				},

				Composite{
					Layout: VBox{MarginsZero: true, Spacing: 8},
					Children: []Widget{
						PushButton{
							AssignTo:  &btnMoveUp,
							Text:      "上移",
							Enabled:   false,
							MinSize:   Size{Width: 90},
							OnClicked: func() { sendWithSelected(domain.ActionMoveProfileUp) },
						},
						PushButton{
							AssignTo:  &btnMoveDown,
							Text:      "下移",
							Enabled:   false,
							MinSize:   Size{Width: 90},
							OnClicked: func() { sendWithSelected(domain.ActionMoveProfileDown) },
						},
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
		if item.IsActive {
			return "✔ 使用中"
		}
		return ""
	case 1:
		return item.Name
	case 2:
		return filepath.Base(item.Path)
	case 3:
		if item.IsRemote {
			return "订阅配置"
		}
		return "本地配置"
	case 4:
		if !item.IsRemote {
			return "-"
		}
		if item.Interval > 0 {
			return fmt.Sprintf("%d 天", item.Interval)
		}
		return "停止更新"
	case 5:
		if !item.IsRemote {
			return "-"
		}
		return item.LastUpdate
	}
	return ""
}
