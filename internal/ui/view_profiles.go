package ui

import (
	"fmt"
	"path/filepath"

	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"

	"mihomo-tray/internal/domain"
)

type ProfileView struct {
	engine         *Engine
	tableView      *walk.TableView
	model          *ProfileModel
	portsLabel     *walk.Label

	actionSwitch   *walk.Action
	actionEditText *walk.Action
	actionEditInfo *walk.Action
	actionUpdate   *walk.Action
	actionDelete   *walk.Action
	actionMoveUp   *walk.Action
	actionMoveDown *walk.Action
	btnMoveUp      *walk.PushButton
	btnMoveDown    *walk.PushButton

	isBusy         bool
}

type ProfileModel struct {
	walk.TableModelBase
	Items        []domain.UIProfileItem
	IsBusy       bool
	TargetRowIdx int
}

func NewProfileView(e *Engine) *ProfileView {
	return &ProfileView{
		engine: e,
		model:  &ProfileModel{Items: []domain.UIProfileItem{}, TargetRowIdx: -1},
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
					OnItemActivated: func() {
						if v.isBusy {
							return
						}
						sendWithSelected(domain.ActionSwitchProfile)
					},
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
	forceRedrawRow := v.syncBusyState(state.IsBusy)
	v.updatePortsLabel(state)
	v.renderTableItems(state.ProfileItems, forceRedrawRow)
	v.updateActionState()
}

func (v *ProfileView) syncBusyState(newBusy bool) int {
	forceRedrawRow := -1
	wasBusy := v.isBusy
	v.isBusy = newBusy

	if v.model == nil {
		return forceRedrawRow
	}
	v.model.IsBusy = newBusy

	if newBusy {
		if !wasBusy && v.tableView != nil {
			v.model.TargetRowIdx = v.tableView.CurrentIndex()
			v.tableView.SetCurrentIndex(-1)
		}
		forceRedrawRow = v.model.TargetRowIdx
	} else {
		if wasBusy && v.model.TargetRowIdx >= 0 && v.tableView != nil {
			v.tableView.SetCurrentIndex(v.model.TargetRowIdx)
			forceRedrawRow = v.model.TargetRowIdx
		}
		v.model.TargetRowIdx = -1
	}
	return forceRedrawRow
}

func (v *ProfileView) updatePortsLabel(state domain.UIState) {
	if v.portsLabel == nil {
		return
	}
	newPortText := fmt.Sprintf("Mixed  : %d\nSocks  : %d\nHTTP(S): %d", state.MixedPort, state.SocksPort, state.HttpPort)
	if v.portsLabel.Text() != newPortText {
		v.portsLabel.SetText(newPortText)
	}
}

func (v *ProfileView) renderTableItems(items []domain.UIProfileItem, forceRedrawRow int) {
	if v.tableView == nil {
		v.model.Items = items
		return
	}

	if v.isStructurallyEqual(v.model.Items, items) {
		v.fastUpdateTable(items, forceRedrawRow)
		return
	}

	v.fullRebuildTable(items, forceRedrawRow)
}

func (v *ProfileView) fastUpdateTable(items []domain.UIProfileItem, forceRedrawRow int) {
	for i := range items {
		old := v.model.Items[i]
		cur := items[i]
		if old.IsActive != cur.IsActive || old.Interval != cur.Interval || old.LastUpdate != cur.LastUpdate || old.Name != cur.Name {
			v.model.Items[i] = cur
			v.model.PublishRowChanged(i)
		}
	}

	if forceRedrawRow >= 0 {
		v.model.PublishRowChanged(forceRedrawRow)
	}
}

func (v *ProfileView) fullRebuildTable(items []domain.UIProfileItem, forceRedrawRow int) {
	var selectedPath string

	realIdx := v.tableView.CurrentIndex()
	if realIdx == -1 && forceRedrawRow >= 0 {
		realIdx = forceRedrawRow
	}

	if realIdx >= 0 && realIdx < len(v.model.Items) {
		selectedPath = v.model.Items[realIdx].Path
	}

	v.tableView.SetSuspended(true)
	defer v.tableView.SetSuspended(false)

	v.model.Items = items
	v.model.PublishRowsReset()

	if selectedPath != "" {
		found := false
		for i, item := range items {
			if item.Path == selectedPath {
				v.tableView.SetCurrentIndex(i)
				if v.model.IsBusy {
					v.model.TargetRowIdx = i
				}
				found = true
				break
			}
		}
		if !found && v.model.IsBusy {
			v.model.TargetRowIdx = -1
		}
	} else if v.model.IsBusy {
		v.model.TargetRowIdx = -1
	}
}

func (v *ProfileView) isStructurallyEqual(oldItems, newItems []domain.UIProfileItem) bool {
	if len(oldItems) != len(newItems) {
		return false
	}
	for i := range oldItems {
		if oldItems[i].Path != newItems[i].Path || oldItems[i].IsRemote != newItems[i].IsRemote {
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
	if hasSelection && !v.isBusy {
		item := v.model.Items[idx]
		canSwitch = !item.IsActive
		canUpdate = item.IsRemote
		canMoveUp = idx > 0
		canMoveDown = idx < len(v.model.Items)-1
	}

	safelySetEnabled(v.actionSwitch, canSwitch)
	safelySetEnabled(v.actionEditText, hasSelection && !v.isBusy)
	safelySetEnabled(v.actionEditInfo, hasSelection && !v.isBusy)
	safelySetEnabled(v.actionUpdate, canUpdate)
	safelySetEnabled(v.actionDelete, hasSelection && !v.isBusy)
	safelySetEnabled(v.actionMoveUp, canMoveUp)
	safelySetEnabled(v.actionMoveDown, canMoveDown)

	if v.btnMoveUp != nil {
		v.btnMoveUp.SetEnabled(canMoveUp)
	}
	if v.btnMoveDown != nil {
		v.btnMoveDown.SetEnabled(canMoveDown)
	}
}

func (m *ProfileModel) RowCount() int {
	return len(m.Items)
}

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

func (m *ProfileModel) StyleCell(style *walk.CellStyle) {
	if m.IsBusy && style.Row() == m.TargetRowIdx {
		style.BackgroundColor = walk.RGB(160, 160, 160)
	}
}
