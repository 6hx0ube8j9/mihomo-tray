package ui

import (
	"fmt"
	"log/slog"
	"syscall"

	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"
	"github.com/tailscale/win"

	"mihomo-tray/internal/domain"
)

func centerWindow(win *walk.MainWindow) {
	if win == nil {
		return
	}

	monitor := walk.PrimaryMonitor()
	workArea := monitor.WorkArea()
	bounds := win.Bounds()

	newX := workArea.X + (workArea.Width-bounds.Width)/2
	newY := workArea.Y + (workArea.Height-bounds.Height)/2

	if newX < 0 {
		newX = 0
	}
	if newY < 0 {
		newY = 0
	}

	win.SetBounds(walk.Rectangle{
		X:      newX,
		Y:      newY,
		Width:  bounds.Width,
		Height: bounds.Height,
	})
}

type ProfileModel struct {
	walk.TableModelBase
	Items []domain.UIProfileItem
}

func (m *ProfileModel) RowCount() int {
	return len(m.Items)
}

func (m *ProfileModel) Value(row, col int) interface{} {
	item := m.Items[row]
	switch col {
	case 0:
		if item.IsActive {
			return "✔️ 使用中"
		}
		return " "
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

func (e *UIEngine) ShowProfileManager(items []domain.UIProfileItem) {
	e.app.Synchronize(func() {
		if e.panelWindow == nil {
			e.panelModel = &ProfileModel{Items: items}

			// 预先声明控件指针
			var actionSwitch, actionEditText, actionEditSub, actionUpdate *walk.Action
			var actionMoveUp, actionMoveDown, actionDelete *walk.Action
			var btnMoveUp, btnMoveDown *walk.PushButton

			// 状态控制器：集中管理所有按钮/菜单的可用状态
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
					if btnMoveUp != nil { btnMoveUp.SetEnabled(false) }
					if btnMoveDown != nil { btnMoveDown.SetEnabled(false) }
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
				if btnMoveUp != nil { btnMoveUp.SetEnabled(canMoveUp) }
				if btnMoveDown != nil { btnMoveDown.SetEnabled(canMoveDown) }
			}

			// 声明式 UI 树 (流式布局优化版)
			err := MainWindow{
				AssignTo: &e.panelWindow,
				Title:    "配置管理",
				MinSize:  Size{Width: 700, Height: 350}, // 赋予合理的最小下限
				Font:     Font{Family: "Microsoft YaHei", PointSize: 10},
				Layout:   VBox{Margins: Margins{Left: 15, Top: 15, Right: 15, Bottom: 15}, Spacing: 10},
				Children: []Widget{
					
					// 顶部工具栏
					Composite{
						Layout: HBox{MarginsZero: true, Spacing: 10},
						Children: []Widget{
							PushButton{
								Text: "➕ 添加远程订阅",
								OnClicked: func() { e.sendCommand(domain.ActionRequestAddRemote, "") },
							},
							PushButton{
								Text: "📂 导入本地配置",
								OnClicked: func() { e.sendCommand(domain.ActionRequestAddLocal, "") },
							},
							HSpacer{}, // 将按钮挤到左侧
						},
					},

					// 核心内容区：表格 (左) + 操作列 (右)
					Composite{
						Layout: HBox{MarginsZero: true, Spacing: 10},
						Children: []Widget{
							
							// 左侧自适应表格
							TableView{
								AssignTo: &e.tableView,
								Columns: []TableViewColumn{
									{Title: "状态", Width: 90},
									{Title: "名称", Width: 220},
									{Title: "类型", Width: 80},
									{Title: "更新频率", Width: 100},
									{Title: "上次更新", Width: 130},
								},
								Model: e.panelModel,
								OnCurrentIndexChanged: updateActionState, // 绑定独立的状态控制器
								ContextMenuItems: []MenuItem{
									Action{
										AssignTo: &actionSwitch,
										Text:     "✔️ 切换配置",
										OnTriggered: func() {
											if idx := e.tableView.CurrentIndex(); idx >= 0 {
												e.sendCommand(domain.ActionSwitchProfile, e.panelModel.Items[idx].Path)
											}
										},
									},
									Action{
										AssignTo: &actionEditText,
										Text:     "📝 打开文本",
										OnTriggered: func() {
											if idx := e.tableView.CurrentIndex(); idx >= 0 {
												e.sendCommand(domain.ActionOpenConfigFile, e.panelModel.Items[idx].Path)
											}
										},
									},
									Action{
										AssignTo: &actionEditSub,
										Text:     "⚙️ 编辑订阅",
										OnTriggered: func() {
											if idx := e.tableView.CurrentIndex(); idx >= 0 {
												e.sendCommand(domain.ActionRequestEditRemote, e.panelModel.Items[idx].Path)
											}
										},
									},
									Action{
										AssignTo: &actionUpdate,
										Text:     "🔄 立即更新",
										OnTriggered: func() {
											if idx := e.tableView.CurrentIndex(); idx >= 0 {
												e.sendCommand(domain.ActionUpdateRemoteProfile, e.panelModel.Items[idx].Path)
											}
										},
									},
									Separator{},
									Action{
										AssignTo: &actionMoveUp,
										Text:     "⬆️ 向上移动",
										OnTriggered: func() {
											if idx := e.tableView.CurrentIndex(); idx >= 0 {
												e.sendCommand(domain.ActionMoveProfileUp, e.panelModel.Items[idx].Path)
											}
										},
									},
									Action{
										AssignTo: &actionMoveDown,
										Text:     "⬇️ 向下移动",
										OnTriggered: func() {
											if idx := e.tableView.CurrentIndex(); idx >= 0 {
												e.sendCommand(domain.ActionMoveProfileDown, e.panelModel.Items[idx].Path)
											}
										},
									},
									Separator{},
									Action{
										AssignTo: &actionDelete,
										Text:     "❌ 删除配置",
										OnTriggered: func() {
											if idx := e.tableView.CurrentIndex(); idx >= 0 {
												e.sendCommand(domain.ActionRemoveProfile, e.panelModel.Items[idx].Path)
											}
										},
									},
								},
							},

							// 右侧动作按钮区
							Composite{
								Layout: VBox{MarginsZero: true, Spacing: 8},
								Children: []Widget{
									PushButton{
										AssignTo: &btnMoveUp,
										Text:     "⬆️ 上移",
										Enabled:  false,
										MinSize:  Size{Width: 90}, // 仅设定最小宽度，高度自适应
										OnClicked: func() {
											if idx := e.tableView.CurrentIndex(); idx >= 0 {
												e.sendCommand(domain.ActionMoveProfileUp, e.panelModel.Items[idx].Path)
											}
										},
									},
									PushButton{
										AssignTo: &btnMoveDown,
										Text:     "⬇️ 下移",
										Enabled:  false,
										MinSize:  Size{Width: 90},
										OnClicked: func() {
											if idx := e.tableView.CurrentIndex(); idx >= 0 {
												e.sendCommand(domain.ActionMoveProfileDown, e.panelModel.Items[idx].Path)
											}
										},
									},
									VSpacer{}, // 将按钮死死顶在上方
								},
							},
						},
					},
				},
			}.Create()

			if err != nil {
				slog.Error("创建配置面板主窗口失败", "err", err)
				return
			}

			// 拦截关闭事件，转为隐藏
			var oldWndProc uintptr
			newWndProc := syscall.NewCallback(func(hwnd win.HWND, msg uint32, wParam, lParam uintptr) uintptr {
				if msg == win.WM_CLOSE {
					win.ShowWindow(hwnd, win.SW_HIDE)
					return 0
				}
				return win.CallWindowProc(oldWndProc, hwnd, msg, wParam, lParam)
			})
			oldWndProc = win.SetWindowLongPtr(e.panelWindow.Handle(), win.GWLP_WNDPROC, newWndProc)

			centerWindow(e.panelWindow)
		}

		e.panelModel.Items = items
		e.panelModel.PublishRowsReset()
		if e.tableView != nil {
			e.tableView.SetCurrentIndex(-1)
		}

		hwnd := e.panelWindow.Handle()
		if win.IsIconic(hwnd) {
			win.ShowWindow(hwnd, win.SW_RESTORE)
		}

		if !e.panelWindow.Visible() {
			e.panelWindow.Show()
		}

		win.SetForegroundWindow(hwnd)
		e.panelWindow.SetFocus()
	})
}

func (e *UIEngine) RefreshPanelData(items []domain.UIProfileItem) {
	if e.app == nil || e.panelWindow == nil || !e.panelWindow.Visible() {
		return
	}

	e.app.Synchronize(func() {
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
