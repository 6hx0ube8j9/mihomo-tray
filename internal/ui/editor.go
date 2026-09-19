package ui

import (
	"net/url"
	"strings"

	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"
	
	"mihomo-tray/internal/domain"
)

var isSubscriptionEditorOpen bool

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
		if isSubscriptionEditorOpen {
			resCh <- result{ok: false}
			return
		}
		isSubscriptionEditorOpen = true

		var dlg *walk.Dialog
		var nameEdit *walk.LineEdit
		var urlEdit *walk.LineEdit
		var intervalEdit *walk.NumberEdit
		var acceptButton *walk.PushButton

		var outName, outUrl string
		var outInterval int
		var accepted bool

		var owner walk.Form = getValidOwner()

		dialogDecl := Dialog{
			AssignTo: &dlg,
			Title:    title,
			MinSize:  Size{Width: 450, Height: 220},
			Layout:   VBox{},
			Children: []Widget{
				Composite{
					Layout: Grid{Columns: 2},
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
								Label{Text: "天 (填 0 为禁用自动更新)"},
								HSpacer{},
							},
						},
					},
				},
				Composite{
					Layout: HBox{},
					Children: []Widget{
						HSpacer{},
						PushButton{
							AssignTo: &acceptButton,
							Text:     "确定",
							OnClicked: func() {
								name := strings.TrimSpace(nameEdit.Text())
								inputUrl := strings.TrimSpace(urlEdit.Text())

								if inputUrl == "" {
									RunErrorDialog(dlg, "输入错误", "订阅链接不能为空！")
									return
								}

								u, parseErr := url.ParseRequestURI(inputUrl)
								if parseErr != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
									RunErrorDialog(dlg, "输入错误", "请输入有效且合法的 HTTP/HTTPS 订阅链接！")
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
							Text: "取消",
							OnClicked: func() {
								dlg.Cancel()
							},
						},
					},
				},
			},
		}

		err := dialogDecl.Create(owner)

		if err != nil {
			isSubscriptionEditorOpen = false
			resCh <- result{ok: false}
			return
		}

		dlg.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
			isSubscriptionEditorOpen = false

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
