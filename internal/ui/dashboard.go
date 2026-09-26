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

// 辅助：窗口居中
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

func (e *UIEngine) InitDashboardWindow() error {
	e.panelModel = &ProfileModel{
		Items: []ProfileItem{},
	}

	var actionSwitch, actionEditText, actionEditSub, actionUpdate *walk.Action
	var actionMoveUp, actionMoveDown, actionDelete *walk.Action
	var btnMoveUp, btnMoveDown *walk.PushButton
	var btnAddRemote, btnAddLocal *walk.PushButton

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
		Title:    "Mihomo Tray",
		MinSize:  Size{Width: 700, Height: 350},
		Size:     Size{Width: 750, Height: 400},
		Font:     Font{Family: "Microsoft YaHei", PointSize: 10},
		Layout:   VBox{Margins: Margins{Left: 15, Top: 15, Right: 15, Bottom: 15}, Spacing: 10},
		Children: []Widget{
			// 1. 顶部操作栏：设置 MinSize 支撑高度，防止 PushButton 被压缩截断
			Composite{
				MinSize: Size{Height: 36},
				Layout:  HBox{Margins: Margins{Left: 0, Top: 0, Right: 0, Bottom: 0}, Spacing: 10},
				Children: []Widget{
					PushButton{
						AssignTo:  &btnAddRemote,
						MinSize:   Size{Height: 30},
						Text:      "➕ 添加远程订阅",
						OnClicked: func() {},
					},
					PushButton{
						AssignTo:  &btnAddLocal,
						MinSize:   Size{Height: 30},
						Text:      "📂 导入本地配置",
						OnClicked: func() {},
					},
					HSpacer{},
					Label{
						Text: "",
					},
				},
			},
			// 2. 主体表格与侧边移动按钮
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
							PushButton{AssignTo: &btnMoveUp, Text: "⬆️ 上移", Enabled: false, MinSize: Size{Width: 90, Height: 30}, OnClicked: func() {}},
							PushButton{AssignTo: &btnMoveDown, Text: "⬇️ 下移", Enabled: false, MinSize: Size{Width: 90, Height: 30}, OnClicked: func() {}},
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

	centerWindow(e.dashboardWindow)
	updateActionState()

	return nil
}


func (e *UIEngine) RefreshPanelData(items []ProfileItem) {
	if e.panelModel == nil {
		return
	}
	e.panelModel.Items = items
	e.panelModel.PublishRowsReset()
}
