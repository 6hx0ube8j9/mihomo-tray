package ui

import (
	"net/url"
	"strings"

	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"
	
	"mihomo-tray/internal/domain"
)

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
		// 无全局锁机制：通过嗅探当前活动窗口，动态拦截重复呼出
		if active := e.app.ActiveForm(); active != nil {
			if dlg, isDialog := active.(*walk.Dialog); isDialog && dlg.Title() == title {
				dlg.SetFocus() // 将已存在的对话框闪烁并推向最前
				resCh <- result{ok: false}
				return
			}
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

		dlg.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
			// 对话框关闭时，安全交还焦点给主面板
			if e.panelWindow != nil && e.panelWindow.Visible() && getValidOwner() != nil {
				e.panelWindow.Show()
				e.panelWindow.SetFocus()
			}
		})

		dlg.Run()

		resCh <- result{outName, outUrl, outInterval, accepted}
	})

	res := <-resCh
	return res.name, res.url, res.interval, res.ok
}
