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
			return "✔️ 正在使用"
		}
		return " "
	case 1:
		return item.Name
	case 2:
		if item.IsRemote {
			return "远程订阅"
		}
		return "本地配置"
	case 3:
		if !item.IsRemote {
			return "-"
		}
		if item.Interval > 0 {
			return fmt.Sprintf("%d 天", item.Interval)
		}
		return "禁用自动更新"
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

			var actionSwitch *walk.Action
			var actionEditText *walk.Action
			var actionEditSub *walk.Action
			var actionUpdate *walk.Action
			var actionMoveUp *walk.Action
			var actionMoveDown *walk.Action
			var actionDelete *walk.Action

			var btnMoveUp *walk.PushButton
			var btnMoveDown *walk.PushButton

			err := MainWindow{
				AssignTo: &e.panelWindow,
				Title:    "管理配置",
				MinSize:  Size{Width: 700, Height: 300},
				Size:     Size{Width: 750, Height: 350},
				Font:     Font{Family: "Microsoft YaHei", PointSize: 10},
				Layout:   VBox{Margins: Margins{Left: 15, Top: 15, Right: 15, Bottom: 15}},
				Children: []Widget{
					Composite{
						Layout: HBox{MarginsZero: true},
						Children: []Widget{
							PushButton{
								Text: "➕ 添加远程订阅",
								OnClicked: func() {
									e.sendCommand("RequestAddRemoteProfile", "")
								},
							},
							PushButton{
								Text: "📂 导入本地配置",
								OnClicked: func() {
									e.sendCommand("RequestAddLocalProfile", "")
								},
							},
							HSpacer{},
						},
					},
					Composite{
						Layout: HBox{MarginsZero: true},
						Children: []Widget{
							TableView{
								AssignTo: &e.tableView,
								Columns: []TableViewColumn{
									{Title: "状态", Width: 100},
									{Title: "名称", Width: 200},
									{Title: "类型", Width: 90},
									{Title: "更新频率", Width: 110},
									{Title: "上次更新", Width: 140},
								},
								Model: e.panelModel,

								OnCurrentIndexChanged: func() {
									if e.tableView == nil || actionSwitch == nil {
										return
									}
									idx := e.tableView.CurrentIndex()
									if idx < 0 || idx >= len(e.panelModel.Items) {
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
									actionSwitch.SetEnabled(!item.IsActive)
									actionDelete.SetEnabled(!item.IsActive)
									actionEditText.SetEnabled(true)
									actionEditSub.SetEnabled(item.IsRemote)
									actionUpdate.SetEnabled(item.IsRemote)

									canMoveUp := idx > 0
									canMoveDown := idx < len(e.panelModel.Items)-1

									actionMoveUp.SetEnabled(canMoveUp)
									actionMoveDown.SetEnabled(canMoveDown)
									if btnMoveUp != nil {
										btnMoveUp.SetEnabled(canMoveUp)
									}
									if btnMoveDown != nil {
										btnMoveDown.SetEnabled(canMoveDown)
									}
								},

								ContextMenuItems: []MenuItem{
									Action{
										AssignTo: &actionSwitch,
										Text:     "✔️ 切换配置",
										OnTriggered: func() {
											if idx := e.tableView.CurrentIndex(); idx >= 0 {
												e.sendCommand("SwitchProfile", e.panelModel.Items[idx].Path)
											}
										},
									},
									Action{
										AssignTo: &actionEditText,
										Text:     "📝 编辑文本",
										OnTriggered: func() {
											if idx := e.tableView.CurrentIndex(); idx >= 0 {
												e.sendCommand("OpenConfigFile", e.panelModel.Items[idx].Path)
											}
										},
									},
									Action{
										AssignTo: &actionEditSub,
										Text:     "⚙️ 编辑订阅",
										OnTriggered: func() {
											if idx := e.tableView.CurrentIndex(); idx >= 0 {
												e.sendCommand("RequestEditRemoteProfile", e.panelModel.Items[idx].Path)
											}
										},
									},
									Action{
										AssignTo: &actionUpdate,
										Text:     "🔄 立即更新",
										OnTriggered: func() {
											if idx := e.tableView.CurrentIndex(); idx >= 0 {
												e.sendCommand("UpdateRemoteProfile", e.panelModel.Items[idx].Path)
											}
										},
									},
									Separator{},
									Action{
										AssignTo: &actionMoveUp,
										Text:     "⬆️ 向上移动",
										OnTriggered: func() {
											if idx := e.tableView.CurrentIndex(); idx >= 0 {
												e.sendCommand("MoveProfileUp", e.panelModel.Items[idx].Path)
											}
										},
									},
									Action{
										AssignTo: &actionMoveDown,
										Text:     "⬇️ 向下移动",
										OnTriggered: func() {
											if idx := e.tableView.CurrentIndex(); idx >= 0 {
												e.sendCommand("MoveProfileDown", e.panelModel.Items[idx].Path)
											}
										},
									},
									Separator{},
									Action{
										AssignTo: &actionDelete,
										Text:     "❌ 删除配置",
										OnTriggered: func() {
											if idx := e.tableView.CurrentIndex(); idx >= 0 {
												e.sendCommand("RemoveProfile", e.panelModel.Items[idx].Path)
											}
										},
									},
								},
							},
							Composite{
								Layout: VBox{Margins: Margins{Left: 10, Top: 0, Right: 0, Bottom: 0}},
								Children: []Widget{
									PushButton{
										AssignTo: &btnMoveUp,
										Text:     "⬆️ 上移",
										Enabled:  false,
										MinSize:  Size{Width: 85},
										MaxSize:  Size{Width: 85},
										OnClicked: func() {
											if idx := e.tableView.CurrentIndex(); idx >= 0 {
												e.sendCommand("MoveProfileUp", e.panelModel.Items[idx].Path)
											}
										},
									},
									PushButton{
										AssignTo: &btnMoveDown,
										Text:     "⬇️ 下移",
										Enabled:  false,
										MinSize:  Size{Width: 85},
										MaxSize:  Size{Width: 85},
										OnClicked: func() {
											if idx := e.tableView.CurrentIndex(); idx >= 0 {
												e.sendCommand("MoveProfileDown", e.panelModel.Items[idx].Path)
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
				slog.Error("创建配置面板主窗口失败", "err", err)
				return
			}

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
