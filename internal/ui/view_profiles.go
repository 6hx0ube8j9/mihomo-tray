package ui

import (
	"fmt"
	"path/filepath"

	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"
	"github.com/tailscale/win"

	"mihomo-tray/internal/domain"
)

type ProfileView struct {
	engine     *Engine
	tableView  *walk.TableView
	model      *ProfileModel
	portsLabel *walk.Label

	actionSwitch   *walk.Action
	actionEditText *walk.Action
	actionEditInfo *walk.Action
	actionUpdate   *walk.Action
	actionDelete   *walk.Action
	actionMoveUp   *walk.Action
	actionMoveDown *walk.Action
	btnMoveUp      *walk.PushButton
	btnMoveDown    *walk.PushButton
}

func NewProfileView(e *Engine) *ProfileView {
	return &ProfileView{
		engine: e,
		model:  &ProfileModel{Items: []domain.UIProfileItem{}},
	}
}

func (v *ProfileView) Declarative() []Widget {
	sendWithSelected := func(action string) {
		if v.tableView == nil {
			return
		}
		idx := v.tableView.CurrentIndex()
		if idx >= 0 && idx < len(v.model.Items) {
			v.engine.SendCommand(action, v.model.Items[idx].Path)
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
						{Title: "名称", Width: 130},
						{Title: "文件名", Width: 150},
						{Title: "类型", Width: 80, Alignment: AlignCenter},
						{Title: "更新频率", Width: 80, Alignment: AlignCenter},
						{Title: "上次更新", Width: 100, Alignment: AlignCenter},
					},
					Model:                 v.model,
					OnCurrentIndexChanged: v.updateActionState,
					OnItemActivated:       func() { sendWithSelected(domain.ActionSwitchProfile) },
					ContextMenuItems: []MenuItem{
						Action{AssignTo: &v.actionSwitch, Text: "切换配置", OnTriggered: func() { sendWithSelected(domain.ActionSwitchProfile) }},
						Action{AssignTo: &v.actionEditText, Text: "打开文本", OnTriggered: func() { sendWithSelected(domain.ActionOpenConfigFile) }},
						Action{AssignTo: &v.actionEditInfo, Text: "编辑信息", OnTriggered: func() { sendWithSelected(domain.ActionEditProfileInfo) }},
						Action{AssignTo: &v.actionUpdate, Text: "立即更新", OnTriggered: func() { sendWithSelected(domain.ActionUpdateRemoteProfile) }},
						Separator{},
						Action{AssignTo: &v.actionMoveUp, Text: "向上移动", OnTriggered: func() { sendWithSelected(domain.ActionMoveProfileUp) }},
						Action{AssignTo: &v.actionMoveDown, Text: "向下移动", OnTriggered: func() { sendWithSelected(domain.ActionMoveProfileDown) }},
						Separator{},
						Action{AssignTo: &v.actionDelete, Text: "删除配置", OnTriggered: func() { sendWithSelected(domain.ActionRemoveProfile) }},
					},
				},

				Composite{
					Layout: VBox{MarginsZero: true, Spacing: 8},
					Children: []Widget{
						PushButton{
							AssignTo:  &v.btnMoveUp,
							Text:      "上移",
							Enabled:   false,
							MinSize:   Size{Width: 90},
							OnClicked: func() { sendWithSelected(domain.ActionMoveProfileUp) },
						},
						PushButton{
							AssignTo:  &v.btnMoveDown,
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
		newPortText := fmt.Sprintf("Mixed  : %d\nSocks  : %d\nHTTP(S): %d",
			state.MixedPort, state.SocksPort, state.HttpPort)
		if v.portsLabel.Text() != newPortText {
			v.portsLabel.SetText(newPortText)
		}
	}

	items := state.ProfileItems
	if v.tableView == nil {
		v.model.Items = items
		return
	}

	if v.isStructurallyEqual(v.model.Items, items) {
		for i := range items {
			old := v.model.Items[i]
			cur := items[i]
			if old.IsActive != cur.IsActive ||
				old.Interval != cur.Interval ||
				old.LastUpdate != cur.LastUpdate ||
				old.Name != cur.Name {
				v.model.Items[i] = cur
				v.model.PublishRowChanged(i)
			}
		}
		v.updateActionState()
		return
	}

	var selectedPath string
	if idx := v.tableView.CurrentIndex(); idx >= 0 && idx < len(v.model.Items) {
		selectedPath = v.model.Items[idx].Path
	}

	hwnd := v.tableView.Handle()
	if hwnd != 0 {
		win.SendMessage(hwnd, win.WM_SETREDRAW, 0, 0)
	}

	v.model.Items = items
	v.model.PublishRowsReset()

	if selectedPath != "" {
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
	}

	if hwnd != 0 {
		win.SendMessage(hwnd, win.WM_SETREDRAW, 1, 0)
		win.RedrawWindow(hwnd, nil, 0, win.RDW_ERASE|win.RDW_FRAME|win.RDW_INVALIDATE|win.RDW_ALLCHILDREN)
	}

	v.updateActionState()
}

func (v *ProfileView) isStructurallyEqual(oldItems, newItems []domain.UIProfileItem) bool {
	if len(oldItems) != len(newItems) {
		return false
	}
	for i := range oldItems {
		if oldItems[i].Path != newItems[i].Path ||
			oldItems[i].Name != newItems[i].Name ||
			oldItems[i].IsRemote != newItems[i].IsRemote {
			return false
		}
	}
	return true
}

func (v *ProfileView) updateActionState() {
	if v.tableView == nil || v.actionSwitch == nil {
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

	safelySetEnabled(v.actionSwitch, canSwitch)
	safelySetEnabled(v.actionEditText, hasSelection)
	safelySetEnabled(v.actionEditInfo, hasSelection)
	safelySetEnabled(v.actionUpdate, canUpdate)
	safelySetEnabled(v.actionDelete, hasSelection)
	safelySetEnabled(v.actionMoveUp, canMoveUp)
	safelySetEnabled(v.actionMoveDown, canMoveDown)

	if v.btnMoveUp != nil {
		v.btnMoveUp.SetEnabled(canMoveUp)
	}
	if v.btnMoveDown != nil {
		v.btnMoveDown.SetEnabled(canMoveDown)
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
