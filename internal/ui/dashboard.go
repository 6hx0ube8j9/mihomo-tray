package ui

import (
	"fmt"
	"log/slog"

	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"
	"github.com/tailscale/win"

	"mihomo-tray/internal/domain"
)

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

// ==========================================
// 数据模型
// ==========================================

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

// ==========================================
// 仪表盘 (Dashboard) 核心控制器
// ==========================================

func (e *UIEngine) ShowProfileManager(items []domain.UIProfileItem) {
	e.app.Synchronize(func() {
		e.lastProfileItems = items
		e.showDashboard()
	})
}

func (e *UIEngine) showDashboard() {
	if e.dashboardWindow == nil {
		e.panelModel = &ProfileModel{Items: e.lastProfileItems}

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
			Title:    "Mihomo Tray 仪表盘",
			MinSize:  Size{Width: 700, Height: 350},
			Size:     Size{Width: 750, Height: 400},
			Font:     Font{Family: "Microsoft YaHei", PointSize: 10},
			Layout:   VBox{Margins: Margins{Left: 15, Top: 15, Right: 15, Bottom: 15}, Spacing: 10},
			Children: []Widget{
				Composite{
					MinSize: Size{Height: 45},
					MaxSize: Size{Height: 45},
					Layout:  HBox{Margins: Margins{Left: 0, Top: 5, Right: 0, Bottom: 5}, Spacing: 10},
					Children: []Widget{
						PushButton{
							AssignTo:  &btnAddRemote,
							Text:      "➕ 添加远程订阅",
							OnClicked: func() { e.sendCommand(domain.ActionRequestAddRemote, "") },
						},
						PushButton{
							AssignTo:  &btnAddLocal,
							Text:      "📂 导入本地配置",
							OnClicked: func() { e.sendCommand(domain.ActionRequestAddLocal, "") },
						},
						HSpacer{},
					},
				},
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
								Action{
									AssignTo:    &actionSwitch,
									Text:        "✔️ 切换配置",
									OnTriggered: func() {
										if idx := e.tableView.CurrentIndex(); idx >= 0 {
											e.sendCommand(domain.ActionSwitchProfile, e.panelModel.Items[idx].Path)
										}
									},
								},
								Action{
									AssignTo:    &actionEditText,
									Text:        "📝 打开文本",
									OnTriggered: func() {
										if idx := e.tableView.CurrentIndex(); idx >= 0 {
											e.sendCommand(domain.ActionOpenConfigFile, e.panelModel.Items[idx].Path)
										}
									},
								},
								Action{
									AssignTo:    &actionEditSub,
									Text:        "⚙️ 编辑订阅",
									OnTriggered: func() {
										if idx := e.tableView.CurrentIndex(); idx >= 0 {
											e.sendCommand(domain.ActionRequestEditRemote, e.panelModel.Items[idx].Path)
										}
									},
								},
								Action{
									AssignTo:    &actionUpdate,
									Text:        "🔄 立即更新",
									OnTriggered: func() {
										if idx := e.tableView.CurrentIndex(); idx >= 0 {
											e.sendCommand(domain.ActionUpdateRemoteProfile, e.panelModel.Items[idx].Path)
										}
									},
								},
								Separator{},
								Action{
									AssignTo:    &actionMoveUp,
									Text:        "⬆️ 向上移动",
									OnTriggered: func() {
										if idx := e.tableView.CurrentIndex(); idx >= 0 {
											e.sendCommand(domain.ActionMoveProfileUp, e.panelModel.Items[idx].Path)
										}
									},
								},
								Action{
									AssignTo:    &actionMoveDown,
									Text:        "⬇️ 向下移动",
									OnTriggered: func() {
										if idx := e.tableView.CurrentIndex(); idx >= 0 {
											e.sendCommand(domain.ActionMoveProfileDown, e.panelModel.Items[idx].Path)
										}
									},
								},
								Separator{},
								Action{
									AssignTo:    &actionDelete,
									Text:        "❌ 删除配置",
									OnTriggered: func() {
										if idx := e.tableView.CurrentIndex(); idx >= 0 {
											e.sendCommand(domain.ActionRemoveProfile, e.panelModel.Items[idx].Path)
										}
									},
								},
							},
						},
						Composite{
							Layout: VBox{MarginsZero: true, Spacing: 8},
							Children: []Widget{
								PushButton{
									AssignTo:  &btnMoveUp,
									Text:      "⬆️ 上移",
									Enabled:   false,
									MinSize:   Size{Width: 90},
									OnClicked: func() {
										if idx := e.tableView.CurrentIndex(); idx >= 0 {
											e.sendCommand(domain.ActionMoveProfileUp, e.panelModel.Items[idx].Path)
										}
									},
								},
								PushButton{
									AssignTo:  &btnMoveDown,
									Text:      "⬇️ 下移",
									Enabled:   false,
									MinSize:   Size{Width: 90},
									OnClicked: func() {
										if idx := e.tableView.CurrentIndex(); idx >= 0 {
											e.sendCommand(domain.ActionMoveProfileDown, e.panelModel.Items[idx].Path)
										}
									},
								},
								VSpacer{},
							},
						},
					},
				},
			},
		}.Create()

		if err != nil {
			slog.Error("创建仪表盘主窗口失败", "err", err)
			return
		}

		e.dashboardWindow.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
			*canceled = true
			e.dashboardWindow.SetVisible(false)
		})

		centerWindow(e.dashboardWindow)
	}

	hwnd := e.dashboardWindow.Handle()
	if win.IsIconic(hwnd) {
		win.ShowWindow(hwnd, win.SW_RESTORE)
	}
	if !e.dashboardWindow.Visible() {
		e.dashboardWindow.Show()
	}
	e.dashboardWindow.BringToTop()
	e.dashboardWindow.SetFocus()
}

func (e *UIEngine) RefreshPanelData(items []domain.UIProfileItem) {
	if e.app == nil {
		return
	}
	e.app.Synchronize(func() {
		e.lastProfileItems = items
		if e.dashboardWindow == nil || !e.dashboardWindow.Visible() {
			return
		}

		var selectedPath string
		if e.tableView != nil {
			idx := e.tableView.CurrentIndex()
			if idx >= 0 && idx < len(e.panelModel.Items) {
				selectedPath = e.panelModel.Items[idx].Path
			}
		}

		e.panelModel.Items = items
		e.panelModel.PublishRowsReset()

		if e.tableView != nil && selectedPath != "" {
			newIdx := -1
			for i, item := range items {
				if item.Path == selectedPath {
					newIdx = i
					break
				}
			}
			if newIdx >= 0 {
				e.tableView.SetCurrentIndex(newIdx)
			}
			e.tableView.Invalidate()
		}
	})
}

// 占位函数：为未来扩展日志面板保留安全空接口
func (e *UIEngine) AppendLog(msg string) {
	// 目前无日志控件渲染需求，静默丢弃
}
