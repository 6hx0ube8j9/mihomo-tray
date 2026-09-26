package ui

import (
	"fmt"
	"syscall"

	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"
	"github.com/tailscale/win"

	"mihomo-tray/internal/domain"
)

var (
	dashboardNewWndProc uintptr
	dashboardOldWndProc uintptr
)

func centerWindow(winHandle *walk.MainWindow) {
	if winHandle == nil {
		return
	}
	monitor := walk.PrimaryMonitor()
	workArea := monitor.WorkArea()
	bounds := winHandle.Bounds()

	newX := workArea.X + (workArea.Width-bounds.Width)/2
	newY := workArea.Y + (workArea.Height-bounds.Height)/2

	if newX < 0 { newX = 0 }
	if newY < 0 { newY = 0 }

	winHandle.SetBounds(walk.Rectangle{X: newX, Y: newY, Width: bounds.Width, Height: bounds.Height})
}

// ==========================================
// 数据模型 (保持不变)
// ==========================================

type ProfileModel struct {
	walk.TableModelBase
	Items []domain.UIProfileItem
}

func (m *ProfileModel) RowCount() int { return len(m.Items) }

func (m *ProfileModel) Value(row, col int) interface{} {
	item := m.Items[row]
	switch col {
	case 0:
		if item.IsActive { return "使用中" } else { return "" }
	case 1: return item.Name
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

func (e *UIEngine) InitDashboardWindow() error {
	e.panelModel = &ProfileModel{Items: []domain.UIProfileItem{}}

	var btnAddRemote, btnAddLocal *walk.PushButton

	err := MainWindow{
		AssignTo: &e.dashboardWindow,
		Title:    "Mihomo Tray 排查专用",
		MinSize:  Size{Width: 700, Height: 350},
		Size:     Size{Width: 750, Height: 400},
		Visible:  false,
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
					Label{
						Text: "就绪 (UI 骨架测试)",
					},
				},
			},
			Composite{
				Layout: HBox{MarginsZero: true, Spacing: 10},
				Children: []Widget{
					TableView{
						AssignTo: &e.tableView,
						Columns: []TableViewColumn{
							{Title: "状态", Width: 90}, {Title: "名称", Width: 220}, {Title: "类型", Width: 80},
							{Title: "更新频率", Width: 100}, {Title: "上次更新", Width: 130},
						},
						Model: e.panelModel,
					},
				},
			},
		},
	}.Create()

	if err != nil {
		return err
	}

	e.mw = e.dashboardWindow
	centerWindow(e.dashboardWindow)

	// 保留防崩溃 Hook
	dashboardNewWndProc = syscall.NewCallback(func(hwnd win.HWND, msg uint32, wParam, lParam uintptr) uintptr {
		if msg == win.WM_CLOSE {
			win.ShowWindow(hwnd, win.SW_HIDE)
			return 0
		}
		return win.CallWindowProc(dashboardOldWndProc, hwnd, msg, wParam, lParam)
	})
	dashboardOldWndProc = win.SetWindowLongPtr(e.dashboardWindow.Handle(), win.GWLP_WNDPROC, dashboardNewWndProc)

	return nil
}

// 维持对接方法不变
func (e *UIEngine) ShowProfileManager(items []domain.UIProfileItem) {
	e.app.Synchronize(func() {
		e.lastProfileItems = items
		e.showDashboard()
	})
}

func (e *UIEngine) showDashboard() {
	if e.dashboardWindow == nil { return }
	hwnd := e.dashboardWindow.Handle()
	if win.IsIconic(hwnd) {
		win.ShowWindow(hwnd, win.SW_RESTORE)
	}
	if !e.dashboardWindow.Visible() {
		e.dashboardWindow.Show()
	}
	win.SetForegroundWindow(hwnd)
	e.dashboardWindow.SetFocus()
}

func (e *UIEngine) RefreshPanelData(items []domain.UIProfileItem) {
	if e.app == nil { return }
	e.app.Synchronize(func() {
		e.lastProfileItems = items
		if e.dashboardWindow == nil || !e.dashboardWindow.Visible() { return }

		e.panelModel.Items = items
		e.panelModel.PublishRowsReset()
		
		if e.tableView != nil {
			e.tableView.Invalidate()
		}
	})
}

func (e *UIEngine) AppendLog(msg string) {}
