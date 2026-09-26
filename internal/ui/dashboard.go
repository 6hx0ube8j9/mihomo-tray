package ui

import (
	"syscall"

	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"
	"github.com/tailscale/win"

	"mihomo-tray/internal/domain"
)

var (
	dashboardNewWndProc uintptr
	dashboardOldWndProc uintptr
	isAppExiting        bool // 强制退出标志
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
// 数据模型 (极简)
// ==========================================

type ProfileModel struct {
	walk.TableModelBase
	Items []domain.UIProfileItem
}
func (m *ProfileModel) RowCount() int { return len(m.Items) }
func (m *ProfileModel) Value(row, col int) interface{} { return "" } // 屏蔽数据渲染，专心排查 UI

func (e *UIEngine) ForceExitApp() {
	isAppExiting = true
	if e.dashboardWindow != nil {
		e.dashboardWindow.Close()
	}
	if e.app != nil {
		e.app.Exit(0)
	}
}

// ==========================================
// 终极排查版：去掉一切容器嵌套和字体约束
// ==========================================

func (e *UIEngine) InitDashboardWindow() error {
	e.panelModel = &ProfileModel{Items: []domain.UIProfileItem{}}

	err := MainWindow{
		AssignTo: &e.dashboardWindow,
		Title:    "Mihomo Tray 终极扒皮测试",
		Size:     Size{Width: 700, Height: 400},

		Layout:   VBox{Margins: Margins{Left: 20, Top: 20, Right: 20, Bottom: 20}, Spacing: 15},
		Children: []Widget{
			PushButton{
				Text: "测试按钮 1：如果我不截断，说明是之前的 Composite 容器引发了坍塌",
				OnClicked: func() { e.sendCommand(domain.ActionRequestAddRemote, "") },
			},
			PushButton{
				Text: "测试按钮 2：导入本地配置",
				OnClicked: func() { e.sendCommand(domain.ActionRequestAddLocal, "") },
			},
			TableView{
				AssignTo: &e.tableView,
				Columns: []TableViewColumn{
					{Title: "状态", Width: 90}, 
					{Title: "名称", Width: 220}, 
				},
				Model: e.panelModel,
			},
		},
	}.Create()

	if err != nil {
		return err
	}

	e.dashboardWindow.Hide()
	e.mw = e.dashboardWindow
	centerWindow(e.dashboardWindow)

	dashboardNewWndProc = syscall.NewCallback(func(hwnd win.HWND, msg uint32, wParam, lParam uintptr) uintptr {
		if msg == win.WM_CLOSE && !isAppExiting {
			win.ShowWindow(hwnd, win.SW_HIDE)
			return 0
		}
		return win.CallWindowProc(dashboardOldWndProc, hwnd, msg, wParam, lParam)
	})
	dashboardOldWndProc = win.SetWindowLongPtr(e.dashboardWindow.Handle(), win.GWLP_WNDPROC, dashboardNewWndProc)

	return nil
}

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

func (e *UIEngine) RefreshPanelData(items []domain.UIProfileItem) {}
func (e *UIEngine) AppendLog(msg string) {}
