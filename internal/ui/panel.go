package ui

import (
	"fmt"
	"log/slog"
	"unsafe"

	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"
	"golang.org/x/sys/windows"
)

var (
	user32DLL               = windows.NewLazySystemDLL("user32.dll")
	procGetSystemMetrics    = user32DLL.NewProc("GetSystemMetrics")
	procSetForegroundWindow = user32DLL.NewProc("SetForegroundWindow")
	procShowWindow          = user32DLL.NewProc("ShowWindow")
	
	procGetWindowRect       = user32DLL.NewProc("GetWindowRect")
	procSetWindowPos        = user32DLL.NewProc("SetWindowPos")
)

const (
	smCXScreen = 0
	smCYScreen = 1
	swHide     = 0
	swRestore  = 9
)

type RECT struct {
	Left, Top, Right, Bottom int32
}

func centerWindow(win *walk.Dialog) {
	if win == nil {
		return
	}
	
	cx, _, _ := procGetSystemMetrics.Call(uintptr(smCXScreen))
	cy, _, _ := procGetSystemMetrics.Call(uintptr(smCYScreen))

	var r RECT
	procGetWindowRect.Call(uintptr(win.Handle()), uintptr(unsafe.Pointer(&r)))

	width := int(r.Right - r.Left)
	height := int(r.Bottom - r.Top)

	newX := (int(cx) - width) / 2
	newY := (int(cy) - height) / 2

	if newX < 0 {
		newX = 0
	}
	if newY < 0 {
		newY = 0
	}

	procSetWindowPos.Call(uintptr(win.Handle()), 0, uintptr(newX), uintptr(newY), 0, 0, 0x0005)
}

type ProfileModel struct {
	walk.TableModelBase
	Items []ProfileItem
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

func (e *UIEngine) ShowProfileManager(items []ProfileItem) {
	e.app.Synchronize(func() {
		if e.panelWindow == nil {
			e.panelModel = &ProfileModel{Items: items}

			var actionSwitch *walk.Action
			var actionEditText *walk.Action
			var actionEditSub *walk.Action
			var actionUpdate *walk.Action
			var actionDelete *walk.Action

			err := Dialog{
				AssignTo: &e.panelWindow,
				Title:    "管理配置",
				MinSize:  Size{Width: 700, Height: 300}, 
				Size:     Size{Width: 750, Height: 350}, 
				Font:     Font{Family: "Microsoft YaHei", PointSize: 10},
				Layout:   VBox{Margins: Margins{Left: 15, Top: 8, Right: 15, Bottom: 15}}, 
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
								actionDelete.SetEnabled(false)
								return
							}

							item := e.panelModel.Items[idx]
							actionSwitch.SetEnabled(!item.IsActive)
							actionDelete.SetEnabled(!item.IsActive)
							actionEditText.SetEnabled(true)
							actionEditSub.SetEnabled(item.IsRemote)
							actionUpdate.SetEnabled(item.IsRemote)
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
				},
			}.Create(e.mw) 

			if err != nil {
				slog.Error("创建配置面板主窗口失败", "err", err)
				return
			}

			centerWindow(e.panelWindow)

			e.panelWindow.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
				*canceled = true
				procShowWindow.Call(uintptr(e.panelWindow.Handle()), swHide)
			})
		}

		e.panelModel.Items = items
		e.panelModel.PublishRowsReset()
		if e.tableView != nil {
			e.tableView.SetCurrentIndex(-1)
		}

		if !e.panelWindow.Visible() {
			centerWindow(e.panelWindow)
			e.panelWindow.Show()
		} else {
			procShowWindow.Call(uintptr(e.panelWindow.Handle()), swRestore)
		}
		
		procSetForegroundWindow.Call(uintptr(e.panelWindow.Handle()))
		e.panelWindow.SetFocus()
	})
}

func (e *UIEngine) RefreshPanelData(items []ProfileItem) {
	if e.app == nil || e.panelWindow == nil || !e.panelWindow.Visible() {
		return
	}
	
	e.app.Synchronize(func() {
		idx := -1
		if e.tableView != nil {
			idx = e.tableView.CurrentIndex()
		}

		e.panelModel.Items = items
		e.panelModel.PublishRowsReset()

		if e.tableView != nil {
			if idx >= 0 && idx < len(items) {
				e.tableView.SetCurrentIndex(idx)
			}
			e.tableView.Invalidate()
		}
	})
}
