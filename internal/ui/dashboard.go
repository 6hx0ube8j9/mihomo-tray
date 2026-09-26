package ui

import (
	"fmt"

	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"
	"github.com/tailscale/win"

	"mihomo-tray/internal/domain"
)

// ProfileModel 表格数据模型
type ProfileModel struct {
	walk.TableModelBase
	Items []domain.UIProfileItem
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

// MainWindowView 独立的仪表盘视图对象，与 UIEngine 解耦
type MainWindowView struct {
	Window     *walk.MainWindow
	TableView  *walk.TableView
	Model      *ProfileModel
	OnCommand  func(action, payload string)
}

func NewMainWindowView(onCommand func(action, payload string)) (*MainWindowView, error) {
	v := &MainWindowView{
		Model: &ProfileModel{
			Items: []domain.UIProfileItem{},
		},
		OnCommand: onCommand,
	}

	var actionSwitch, actionEditText, actionEditSub, actionUpdate *walk.Action
	var actionMoveUp, actionMoveDown, actionDelete *walk.Action
	var btnMoveUp, btnMoveDown *walk.PushButton
	var btnAddRemote, btnAddLocal *walk.PushButton
	var statusLabel *walk.Label

	triggerCmd := func(action, payload string) {
		if v.OnCommand != nil {
			v.OnCommand(action, payload)
		}
	}

	updateActionState := func() {
		if v.TableView == nil || actionSwitch == nil {
			return
		}
		idx := v.TableView.CurrentIndex()
		hasSelection := idx >= 0 && idx < len(v.Model.Items)

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

		item := v.Model.Items[idx]
		canMoveUp := idx > 0
		canMoveDown := idx < len(v.Model.Items)-1

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
		AssignTo: &v.Window,
		Title:    "Mihomo Tray - 配置管理",
		MinSize:  Size{Width: 700, Height: 400},
		Size:     Size{Width: 750, Height: 420},
		Font:     Font{Family: "Microsoft YaHei", PointSize: 10},
		Layout:   VBox{Margins: Margins{Left: 15, Top: 15, Right: 15, Bottom: 15}, Spacing: 10},
		Children: []Widget{
			// 顶部按钮栏：给 Composite 和 PushButton 显式设定高度，免疫布局挤压
			Composite{
				MinSize: Size{Height: 36},
				Layout:  HBox{MarginsZero: true, Spacing: 10},
				Children: []Widget{
					PushButton{
						AssignTo:  &btnAddRemote,
						MinSize:   Size{Width: 120, Height: 32},
						Text:      "➕ 添加远程订阅",
						OnClicked: func() { triggerCmd(domain.ActionRequestAddRemote, "") },
					},
					PushButton{
						AssignTo:  &btnAddLocal,
						MinSize:   Size{Width: 120, Height: 32},
						Text:      "📂 导入本地配置",
						OnClicked: func() { triggerCmd(domain.ActionRequestAddLocal, "") },
					},
					HSpacer{},
					Label{
						AssignTo: &statusLabel,
						Text:     "",
					},
				},
			},
			// 中间表格与右侧按钮栏
			Composite{
				Layout: HBox{MarginsZero: true, Spacing: 10},
				Children: []Widget{
					TableView{
						AssignTo: &v.TableView,
						Columns: []TableViewColumn{
							{Title: "状态", Width: 90},
							{Title: "名称", Width: 220},
							{Title: "类型", Width: 80},
							{Title: "更新频率", Width: 100},
							{Title: "上次更新", Width: 130},
						},
						Model:                 v.Model,
						OnCurrentIndexChanged: updateActionState,
						ContextMenuItems: []MenuItem{
							Action{AssignTo: &actionSwitch, Text: "✔️ 切换配置", OnTriggered: func() {
								if idx := v.TableView.CurrentIndex(); idx >= 0 {
									triggerCmd(domain.ActionSwitchProfile, v.Model.Items[idx].Path)
								}
							}},
							Action{AssignTo: &actionEditText, Text: "📝 打开文本", OnTriggered: func() {
								if idx := v.TableView.CurrentIndex(); idx >= 0 {
									triggerCmd(domain.ActionOpenConfigFile, v.Model.Items[idx].Path)
								}
							}},
							Action{AssignTo: &actionEditSub, Text: "⚙️ 编辑订阅", OnTriggered: func() {
								if idx := v.TableView.CurrentIndex(); idx >= 0 {
									triggerCmd(domain.ActionRequestEditRemote, v.Model.Items[idx].Path)
								}
							}},
							Action{AssignTo: &actionUpdate, Text: "🔄 立即更新", OnTriggered: func() {
								if idx := v.TableView.CurrentIndex(); idx >= 0 {
									triggerCmd(domain.ActionUpdateRemoteProfile, v.Model.Items[idx].Path)
								}
							}},
							Separator{},
							Action{AssignTo: &actionMoveUp, Text: "⬆️ 向上移动", OnTriggered: func() {
								if idx := v.TableView.CurrentIndex(); idx >= 0 {
									triggerCmd(domain.ActionMoveProfileUp, v.Model.Items[idx].Path)
								}
							}},
							Action{AssignTo: &actionMoveDown, Text: "⬇️ 向下移动", OnTriggered: func() {
								if idx := v.TableView.CurrentIndex(); idx >= 0 {
									triggerCmd(domain.ActionMoveProfileDown, v.Model.Items[idx].Path)
								}
							}},
							Separator{},
							Action{AssignTo: &actionDelete, Text: "❌ 删除配置", OnTriggered: func() {
								if idx := v.TableView.CurrentIndex(); idx >= 0 {
									triggerCmd(domain.ActionRemoveProfile, v.Model.Items[idx].Path)
								}
							}},
						},
					},
					Composite{
						Layout: VBox{MarginsZero: true, Spacing: 8},
						Children: []Widget{
							PushButton{AssignTo: &btnMoveUp, Text: "⬆️ 上移", Enabled: false, MinSize: Size{Width: 90, Height: 30}, OnClicked: func() {
								if idx := v.TableView.CurrentIndex(); idx >= 0 {
									triggerCmd(domain.ActionMoveProfileUp, v.Model.Items[idx].Path)
								}
							}},
							PushButton{AssignTo: &btnMoveDown, Text: "⬇️ 下移", Enabled: false, MinSize: Size{Width: 90, Height: 30}, OnClicked: func() {
								if idx := v.TableView.CurrentIndex(); idx >= 0 {
									triggerCmd(domain.ActionMoveProfileDown, v.Model.Items[idx].Path)
								}
							}},
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

	v.Window.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		*canceled = true
		v.Window.SetVisible(false)
	})

	v.centerWindow()
	updateActionState()

	return v, nil
}

func (v *MainWindowView) centerWindow() {
	if v.Window == nil {
		return
	}
	bounds := v.Window.Bounds()
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

	v.Window.SetBounds(walk.Rectangle{
		X:      x,
		Y:      y,
		Width:  bounds.Width,
		Height: bounds.Height,
	})
}

func (v *MainWindowView) Show() {
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
	v.Window.AsFormBase().RequestLayout()
	win.SetForegroundWindow(hwnd)
	v.Window.SetFocus()
}

func (v *MainWindowView) RefreshData(items []domain.UIProfileItem) {
	if v.Model == nil {
		return
	}
	v.Model.Items = items
	v.Model.PublishRowsReset()
}
