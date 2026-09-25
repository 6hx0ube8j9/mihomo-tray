package ui

import (
	"net/url"
	"strings"

	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"
	"github.com/tailscale/win"

	"mihomo-tray/internal/domain"
)

// 记录当前打开的订阅编辑器窗口指针
// 这比单纯的 bool 锁更高级，可以用来精准唤醒被遮挡的弹窗
var currentSubEditor *walk.Dialog

func (e *UIEngine) ShowSubscriptionEditor(title, defaultName, defaultUrl string, defaultInterval int) (string, string, int, bool) {
	if e.app == nil || e.mw == nil {
		return "", "", 0, false
	}

	type result struct {
		name, url string
		interval  int
		ok        bool
	}
	resCh := make(chan result)

	e.app.Synchronize(func() {
		// 真正的防抖与窗口唤醒机制
		if currentSubEditor != nil {
			hwnd := currentSubEditor.Handle()
			if win.IsIconic(hwnd) {
				win.ShowWindow(hwnd, win.SW_RESTORE) // 如果最小化了，恢复它
			}
			win.SetForegroundWindow(hwnd) // 强行拉到最前
			currentSubEditor.SetFocus()   // 给予输入焦点
			resCh <- result{ok: false}
			return
		}

		var dlg *walk.Dialog
		var nameEdit *walk.LineEdit
		var urlEdit *walk.LineEdit
		var intervalEdit *walk.NumberEdit
		var acceptButton *walk.PushButton

		var outName, outUrl string
		var outInterval int
		var accepted bool

		var owner walk.Form = getValidOwner()

		err := Dialog{
			AssignTo: &dlg,
			Title:    title,
			MinSize:  Size{Width: 450, Height: 200},
			Layout:   VBox{Margins: Margins{Left: 15, Top: 15, Right: 15, Bottom: 15}, Spacing: 10},
			Children: []Widget{
				Composite{
					Layout: Grid{Columns: 2, Spacing: 10, MarginsZero: true},
					Children: []Widget{
						Label{Text: "配置名称:"},
						LineEdit{AssignTo: &nameEdit, Text: defaultName},

						Label{Text: "订阅链接:"},
						LineEdit{AssignTo: &urlEdit, Text: defaultUrl},

						Label{Text: "更新频率:"},
						Composite{
							Layout: HBox{MarginsZero: true},
							Children: []Widget{
								NumberEdit{
									AssignTo: &intervalEdit,
									Value:    float64(defaultInterval),
									MinValue: 0,
									MaxValue: float64(domain.MaxUpdateInterval),
								},
								Label{Text: "天 (填 0 为停止自动更新)"},
								HSpacer{}, // 将输入框固定在左侧
							},
						},
					},
				},
				VSpacer{}, // 弹簧：将底部按钮压到底部
				Composite{
					Layout: HBox{MarginsZero: true, Spacing: 10},
					Children: []Widget{
						HSpacer{}, // 弹簧：将按钮挤到右侧
						PushButton{
							AssignTo: &acceptButton,
							Text:     "确定",
							MinSize:  Size{Width: 80},
							OnClicked: func() {
								name := strings.TrimSpace(nameEdit.Text())
								inputUrl := strings.TrimSpace(urlEdit.Text())

								if inputUrl == "" {
									RunErrorDialog(dlg, "输入错误", "订阅链接不能为空！")
									return
								}

								u, parseErr := url.ParseRequestURI(inputUrl)
								if parseErr != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
									RunErrorDialog(dlg, "输入错误", "请输入有效的 HTTP/HTTPS 订阅链接")
									return
								}

								outName = name
								outUrl = inputUrl
								outInterval = int(intervalEdit.Value())
								accepted = true
								dlg.Accept()
							},
						},
						PushButton{
							Text:    "取消",
							MinSize: Size{Width: 80},
							OnClicked: func() {
								dlg.Cancel()
							},
						},
					},
				},
			},
		}.Create(owner)

		if err != nil {
			resCh <- result{ok: false}
			return
		}

		// 创建成功后，记录全局窗口指针
		currentSubEditor = dlg

		dlg.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
			// 窗口关闭时安全释放指针
			currentSubEditor = nil

			// 对话框关闭时，安全交还焦点给主面板仪表盘
			if e.dashboardWindow != nil && e.dashboardWindow.Visible() && getValidOwner() != nil {
				e.dashboardWindow.Show()
				e.dashboardWindow.SetFocus()
			}
		})

		dlg.Run()

		resCh <- result{outName, outUrl, outInterval, accepted}
	})

	res := <-resCh
	return res.name, res.url, res.interval, res.ok
}
