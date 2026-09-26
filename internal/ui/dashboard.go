package ui

import (
	"fmt"
	"walk-app/internal/types"

	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"
	"github.com/tailscale/win"
)

// ProfileItem 数据项结构
type ProfileItem struct {
	IsActive   bool
	Name       string
	IsRemote   bool
	Interval   int
	LastUpdate string
	Path       string
}

// ProfileModel 表格数据模型
type ProfileModel struct {
	walk.TableModelBase
	Items []ProfileItem
}

func (m *ProfileModel) RowCount() int { return len(m.Items) }

func (m *ProfileModel) Value(row, col int) interface{} {
	if row < 0 || row >= len(m.Items) { return "" }
	item := m.Items[row]
	switch col {
	case 0:
		if item.IsActive { return "使用中" } else { return "" }
	case 1:
		return item.Name
	case 2:
		if item.IsRemote { return "订阅配置" } else { return "本地配置" }
	case 3:
		if !item.IsRemote { return "-" }
		if item.Interval > 0 { return fmt.Sprintf("%d 天", item.Interval) }
		return "停止更新"
	case 4:
		if !item.IsRemote { return "-" }
		return item.LastUpdate
	}
	return ""
}

type MainWindowView struct {
	Window      *walk.MainWindow
	TableView   *walk.TableView
	Model       *ProfileModel
	StatusLabel *walk.Label
}

func (v *MainWindowView) Wake() {
	if v.Window == nil { return }
	if !v.Window.Visible() {
		v.Window.Show()
	}
	v.Window.BringToTop()
	v.Window.SetFocus()
}

// 标准居中计算
func centerWindow(winHandle *walk.MainWindow) {
	if winHandle == nil { return }
	bounds := winHandle.Bounds()
	screenW, screenH := int(win.GetSystemMetrics(win.SM_CXSCREEN)), int(win.GetSystemMetrics(win.SM_CYSCREEN))
	x, y := (screenW-bounds.Width)/2, (screenH-bounds.Height)/2
	if x < 0 { x = 0 }
	if y < 0 { y = 0 }
	winHandle.SetBounds(walk.Rectangle{X: x, Y: y, Width: bounds.Width, Height: bounds.Height})
}

func CreateMainWindow(cmdCh chan<- types.UICommand, title string) (*MainWindowView, error) {
	view := &MainWindowView{
		Model: &ProfileModel{
			Items: []ProfileItem{
				{IsActive: true, Name: "示例节点订阅 - 香港", IsRemote: true, Interval: 1, LastUpdate: "2026-03-30 10:00", Path: "sub1"},
				{IsActive: false, Name: "本地自建备用节点", IsRemote: false, Interval: 0, LastUpdate: "-", Path: "local1"},
				{IsActive: false, Name: "团队公共订阅 - 日本", IsRemote: true, Interval: 7, LastUpdate: "2026-03-28 14:20", Path: "sub2"},
			},
		},
	}

	sendCmd := func(action string) {
		if cmdCh == nil { return }
		select {
		case cmdCh <- types.UICommand{Action: action}:
		default:
		}
	}

	var actionSwitch, actionEditText, actionEditSub, actionUpdate *walk.Action
	var actionMoveUp, actionMoveDown, actionDelete *walk.Action
	var btnMoveUp, btnMoveDown *walk.PushButton
	var btnAddRemote, btnAddLocal *walk.PushButton

	updateActionState := func() {
		if view.TableView == nil || actionSwitch == nil { return }
		idx := view.TableView.CurrentIndex()
		hasSelection := idx >= 0 && idx < len(view.Model.Items)

		if !hasSelection {
			actionSwitch.SetEnabled(false); actionEditText.SetEnabled(false); actionEditSub.SetEnabled(false); actionUpdate.SetEnabled(false)
			actionMoveUp.SetEnabled(false); actionMoveDown.SetEnabled(false); actionDelete.SetEnabled(false)
			if btnMoveUp != nil { btnMoveUp.SetEnabled(false) }
			if btnMoveDown != nil { btnMoveDown.SetEnabled(false) }
			return
		}

		item := view.Model.Items[idx]
		canMoveUp, canMoveDown := idx > 0, idx < len(view.Model.Items)-1

		actionSwitch.SetEnabled(!item.IsActive); actionDelete.SetEnabled(!item.IsActive)
		actionEditText.SetEnabled(true); actionEditSub.SetEnabled(item.IsRemote); actionUpdate.SetEnabled(item.IsRemote)
		actionMoveUp.SetEnabled(canMoveUp); actionMoveDown.SetEnabled(canMoveDown)
		if btnMoveUp != nil { btnMoveUp.SetEnabled(canMoveUp) }
		if btnMoveDown != nil { btnMoveDown.SetEnabled(canMoveDown) }
	}

	err := MainWindow{
		AssignTo: &view.Window,
		Title:    title,
		MinSize:  Size{Width: 700, Height: 350},
		Size:     Size{Width: 750, Height: 400},
		Font:     Font{Family: "Microsoft YaHei", PointSize: 10},
		Layout:   VBox{Margins: Margins{Left: 15, Top: 15, Right: 15, Bottom: 15}, Spacing: 10},
		Children: []Widget{
			Composite{
				MinSize: Size{Height: 40},
				Layout:  HBox{Margins: Margins{Left: 0, Top: 2, Right: 0, Bottom: 2}, Spacing: 10},
				Children: []Widget{
					PushButton{
						AssignTo:  &btnAddRemote,
						Text:      "➕ 添加远程订阅",
						OnClicked: func() { sendCmd("add_remote") },
					},
					PushButton{
						AssignTo:  &btnAddLocal,
						Text:      "📂 导入本地配置",
						OnClicked: func() { sendCmd("add_local") },
					},
					HSpacer{},
					Label{
						AssignTo: &view.StatusLabel,
						Text:     "就绪 (GUI 骨架测试)",
					},
				},
			},
			Composite{
				Layout: HBox{MarginsZero: true, Spacing: 10},
				Children: []Widget{
					TableView{
						AssignTo: &view.TableView,
						Columns: []TableViewColumn{
							{Title: "状态", Width: 90}, {Title: "名称", Width: 220}, {Title: "类型", Width: 80},
							{Title: "更新频率", Width: 100}, {Title: "上次更新", Width: 130},
						},
						Model:                 view.Model,
						OnCurrentIndexChanged: updateActionState,
						ContextMenuItems: []MenuItem{
							Action{AssignTo: &actionSwitch, Text: "✔️ 切换配置", OnTriggered: func() { sendCmd("switch_profile") }},
							Action{AssignTo: &actionEditText, Text: "📝 打开文本", OnTriggered: func() { sendCmd("edit_text") }},
							Action{AssignTo: &actionEditSub, Text: "⚙️ 编辑订阅", OnTriggered: func() { sendCmd("edit_sub") }},
							Action{AssignTo: &actionUpdate, Text: "🔄 立即更新", OnTriggered: func() { sendCmd("update_remote") }},
							Separator{},
							Action{AssignTo: &actionMoveUp, Text: "⬆️ 向上移动", OnTriggered: func() { sendCmd("move_up") }},
							Action{AssignTo: &actionMoveDown, Text: "⬇️ 向下移动", OnTriggered: func() { sendCmd("move_down") }},
							Separator{},
							Action{AssignTo: &actionDelete, Text: "❌ 删除配置", OnTriggered: func() { sendCmd("delete_profile") }},
						},
					},
					Composite{
						Layout: VBox{MarginsZero: true, Spacing: 8},
						Children: []Widget{
							PushButton{AssignTo: &btnMoveUp, Text: "⬆️ 上移", Enabled: false, MinSize: Size{Width: 90}, OnClicked: func() { sendCmd("move_up") }},
							PushButton{AssignTo: &btnMoveDown, Text: "⬇️ 下移", Enabled: false, MinSize: Size{Width: 90}, OnClicked: func() { sendCmd("move_down") }},
							VSpacer{},
						},
					},
				},
			},
		},
	}.Create()

	if err != nil {
		return nil, err
	}

	view.Window.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		*canceled = true
		view.Window.SetVisible(false)
	})

	centerWindow(view.Window)
	updateActionState()

	return view, nil
}
