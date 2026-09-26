package ui

import (
	"fmt"

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

type MainWindowView struct {
	Window      *walk.MainWindow
	TableView   *walk.TableView
	Model       *ProfileModel
	StatusLabel *walk.Label
}

func (v *MainWindowView) Wake() {
	if v.Window == nil {
		return
	}
	hwnd := v.Window.Handle()
	if win.IsIconic(hwnd) {
		win.ShowWindow(hwnd, win.SW_RESTORE)
	}
	if !v.Window.Visible() {
		v.Window.Show()
	}
	win.SetForegroundWindow(hwnd)
	v.Window.SetFocus()
}

func centerWindow(winHandle *walk.MainWindow) {
	if winHandle == nil {
		return
	}
	bounds := winHandle.Bounds()
	screenW := int(win.GetSystemMetrics(win.SM_CXSCREEN))
	screenH := int(win.GetSystemMetrics(win.SM_CYSCREEN))

	x := (screenW - bounds.Width) / 2
	y := (screenH - bounds.Height) / 2

	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}

	winHandle.SetBounds(walk.Rectangle{
		X:      x,
		Y:      y,
		Width:  bounds.Width,
		Height: bounds.Height,
	})
}

func CreateMainWindow(title string) (*MainWindowView, error) {
	view := &MainWindowView{
		Model: &ProfileModel{
			Items: []ProfileItem{
				{IsActive: true, Name: "示例节点订阅 - 香港", IsRemote: true, Interval: 1, LastUpdate: "2026-03-30 10:00", Path: "sub1"},
				{IsActive: false, Name: "本地自建备用节点", IsRemote: false, Interval: 0, LastUpdate: "-", Path: "local1"},
				{IsActive: false, Name: "团队公共订阅 - 日本", IsRemote: true, Interval: 7, LastUpdate: "2026-03-28 14:20", Path: "sub2"},
			},
		},
	}

	var actionSwitch, actionEditText, actionEditSub, actionUpdate *walk.Action
	var actionMoveUp, actionMoveDown, actionDelete *walk.Action
	var btnMoveUp, btnMoveDown *walk.PushButton
	var btnAddRemote, btnAddLocal *walk.PushButton

	updateActionState := func() {
		if view.TableView == nil || actionSwitch == nil {
			return
		}
		idx := view.TableView.CurrentIndex()
		hasSelection := idx >= 0 && idx < len(view.Model.Items)

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

		item := view.Model.Items[idx]
		canMoveUp := idx > 0
		canMoveDown := idx < len(view.Model.Items)-1

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
		AssignTo: &view.Window,
		Title:    title,
		MinSize:  Size{Width: 700, Height: 350},
		Size:     Size{Width: 750, Height: 400},
		Font:     Font{Family: "Microsoft YaHei", PointSize: 10},
		Layout:   VBox{Margins: Margins{Left: 15, Top: 15, Right: 15, Bottom: 15}, Spacing: 10},
		Children: []Widget{
			// 顶部栏：显示约束 MinSize 指定高度 32px 预防按钮挤压截断
			Composite{
				Layout: HBox{Margins: Margins{Left: 0, Top: 5, Right: 0, Bottom: 5}, Spacing: 10},
				Children: []Widget{
					PushButton{
						AssignTo:  &btnAddRemote,
						MinSize:   Size{Height: 32},
						Text:      "➕ 添加远程订阅",
						OnClicked: func() {},
					},
					PushButton{
						AssignTo:  &btnAddLocal,
						MinSize:   Size{Height: 32},
						Text:      "📂 导入本地配置",
						OnClicked: func() {},
					},
					HSpacer{},
					Label{
						AssignTo: &view.StatusLabel,
						Text:     "",
					},
				},
			},
			Composite{
				Layout: HBox{MarginsZero: true, Spacing: 10},
				Children: []Widget{
					TableView{
						AssignTo: &view.TableView,
						Columns: []TableViewColumn{
							{Title: "状态", Width: 90},
							{Title: "名称", Width: 220},
							{Title: "类型", Width: 80},
							{Title: "更新频率", Width: 100},
							{Title: "上次更新", Width: 130},
						},
						Model:                 view.Model,
						OnCurrentIndexChanged: updateActionState,
						ContextMenuItems: []MenuItem{
							Action{AssignTo: &actionSwitch, Text: "✔️ 切换配置", OnTriggered: func() {}},
							Action{AssignTo: &actionEditText, Text: "📝 打开文本", OnTriggered: func() {}},
							Action{AssignTo: &actionEditSub, Text: "⚙️ 编辑订阅", OnTriggered: func() {}},
							Action{AssignTo: &actionUpdate, Text: "🔄 立即更新", OnTriggered: func() {}},
							Separator{},
							Action{AssignTo: &actionMoveUp, Text: "⬆️ 向上移动", OnTriggered: func() {}},
							Action{AssignTo: &actionMoveDown, Text: "⬇️ 向下移动", OnTriggered: func() {}},
							Separator{},
							Action{AssignTo: &actionDelete, Text: "❌ 删除配置", OnTriggered: func() {}},
						},
					},
					Composite{
						Layout: VBox{MarginsZero: true, Spacing: 8},
						Children: []Widget{
							PushButton{AssignTo: &btnMoveUp, Text: "⬆️ 上移", Enabled: false, MinSize: Size{Width: 90, Height: 32}, OnClicked: func() {}},
							PushButton{AssignTo: &btnMoveDown, Text: "⬇️ 下移", Enabled: false, MinSize: Size{Width: 90, Height: 32}, OnClicked: func() {}},
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
