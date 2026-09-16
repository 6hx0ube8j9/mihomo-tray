package view

import (
	"fmt"
	"log/slog"
	"runtime"
	"sync"

	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"

	"mihomo-tray/internal/tray"
)

type ProfileModel struct {
	walk.TableModelBase
	Items []tray.ProfileItem
}

func (m *ProfileModel) RowCount() int {
	return len(m.Items)
}

func (m *ProfileModel) Value(row, col int) interface{} {
	item := m.Items[row]
	switch col {
	case 0:
		if item.IsActive {
			return "[使用中]"
		}
		return ""
	case 1:
		return item.Name
	case 2:
		if item.IsRemote {
			return "(远程订阅)"
		}
		return "(本地)"
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

var (
	panelApp       *walk.Application
	panelWindow    *walk.MainWindow
	tableView      *walk.TableView
	panelModel     *ProfileModel
	globalDispatch func(action, payload string)

	initOnce  sync.Once
	readyChan = make(chan struct{})
)

func RunProfileManager(items []tray.ProfileItem, dispatch func(action, payload string)) error {
	initOnce.Do(func() {
		globalDispatch = dispatch

		go func() {
			runtime.LockOSThread()

			app, err := walk.InitApp()
			if err != nil {
				slog.Error("Walk UI 引擎初始化失败", "err", err)
				return
			}
			panelApp = app
			panelModel = &ProfileModel{Items: make([]tray.ProfileItem, 0)}

			var actionSwitch *walk.Action
			var actionEditText *walk.Action
			var actionEditSub *walk.Action
			var actionUpdate *walk.Action
			var actionDelete *walk.Action

			err = MainWindow{
				AssignTo: &panelWindow,
				Title:    "配置面板",
				MinSize:  Size{Width: 600, Height: 300},
				Size:     Size{Width: 640, Height: 350},
				Layout:   VBox{},
				Children: []Widget{
					Composite{
						Layout: HBox{MarginsZero: true},
						Children: []Widget{
							PushButton{
								Text: "➕ 添加远程订阅",
								OnClicked: func() {
									globalDispatch("RequestAddRemoteProfile", "")
									panelWindow.Hide()
								},
							},
							PushButton{
								Text: "📂 导入本地配置",
								OnClicked: func() {
									globalDispatch("RequestAddLocalProfile", "")
									panelWindow.Hide()
								},
							},
							HSpacer{},
							PushButton{
								Text: "❌ 关闭面板",
								OnClicked: func() {
									panelWindow.Hide()
								},
							},
						},
					},
					TableView{
						AssignTo: &tableView,
						Columns: []TableViewColumn{
							{Title: "状态", Width: 65},
							{Title: "名称", Width: 180},
							{Title: "类型", Width: 80},
							{Title: "更新频率", Width: 100},
							{Title: "上次更新", Width: 120},
						},
						Model: panelModel,

						OnCurrentIndexChanged: func() {
							if tableView == nil || actionSwitch == nil {
								return
							}
							idx := tableView.CurrentIndex()
							if idx < 0 || idx >= len(panelModel.Items) {
								actionSwitch.SetEnabled(false)
								actionEditText.SetEnabled(false)
								actionEditSub.SetEnabled(false)
								actionUpdate.SetEnabled(false)
								actionDelete.SetEnabled(false)
								return
							}

							item := panelModel.Items[idx]
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
									if idx := tableView.CurrentIndex(); idx >= 0 {
										globalDispatch("SwitchProfile", panelModel.Items[idx].Path)
										panelWindow.Hide()
									}
								},
							},
							Action{
								AssignTo: &actionEditText,
								Text:     "📝 编辑文本",
								OnTriggered: func() {
									if idx := tableView.CurrentIndex(); idx >= 0 {
										globalDispatch("OpenConfigFile", panelModel.Items[idx].Path)
									}
								},
							},
							Action{
								AssignTo: &actionEditSub,
								Text:     "⚙️ 编辑订阅",
								OnTriggered: func() {
									if idx := tableView.CurrentIndex(); idx >= 0 {
										globalDispatch("RequestEditRemoteProfile", panelModel.Items[idx].Path)
										panelWindow.Hide()
									}
								},
							},
							Action{
								AssignTo: &actionUpdate,
								Text:     "🔄 立即更新",
								OnTriggered: func() {
									if idx := tableView.CurrentIndex(); idx >= 0 {
										globalDispatch("UpdateRemoteProfile", panelModel.Items[idx].Path)
									}
								},
							},
							Separator{},
							Action{
								AssignTo: &actionDelete,
								Text:     "❌ 删除配置",
								OnTriggered: func() {
									if idx := tableView.CurrentIndex(); idx >= 0 {
										globalDispatch("RemoveProfile", panelModel.Items[idx].Path)
										panelWindow.Hide()
									}
								},
							},
						},
					},
				},
			}.Create()

			if err != nil {
				slog.Error("配置面板构建失败", "err", err)
				return
			}

			panelWindow.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
				*canceled = true
				panelWindow.Hide()
			})

			close(readyChan)
			app.Run()
		}()
	})

	<-readyChan

	if panelApp == nil || panelWindow == nil {
		return fmt.Errorf("walk UI 引擎未就绪")
	}

	// 跨线程安全投递渲染数据
	panelApp.Synchronize(func() {
		panelModel.Items = items
		panelModel.PublishRowsReset()
		tableView.SetCurrentIndex(-1)

		if !panelWindow.Visible() {
			panelWindow.Show()
		}
	})

	return nil
}
