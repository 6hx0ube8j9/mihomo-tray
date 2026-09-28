package ui

import (
	"net/url"
	"strings"

	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"
	"github.com/tailscale/win"

	"mihomo-tray/internal/domain"
)

var currentSubEditor *walk.Dialog

func (e *Engine) ShowSubscriptionEditor(title, defaultName, defaultUrl string, defaultInterval int) (string, string, int, bool) {
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
		if currentSubEditor != nil {
			hwnd := currentSubEditor.Handle()
			if win.IsIconic(hwnd) {
				win.ShowWindow(hwnd, win.SW_RESTORE)
			}
			win.SetForegroundWindow(hwnd)
			currentSubEditor.SetFocus()
			resCh <- result{ok: false}
			return
		}

		var dlg *walk.Dialog
		var nameEdit, urlEdit *walk.LineEdit
		var intervalEdit *walk.NumberEdit
		var acceptButton *walk.PushButton
		var outName, outUrl string
		var outInterval int
		var accepted bool
		var owner walk.Form = getValidOwner()

		err := Dialog{
			AssignTo:      &dlg,
			Title:         title,
			MinSize:       Size{Width: 450, Height: 200},
			DefaultButton: &acceptButton,
			Layout:        VBox{Margins: Margins{Left: 15, Top: 15, Right: 15, Bottom: 15}, Spacing: 10},
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
								NumberEdit{AssignTo: &intervalEdit, Value: float64(defaultInterval), MinValue: 0, MaxValue: float64(domain.MaxUpdateInterval)},
								Label{Text: "天 (填 0 为禁用自动更新)"},
								HSpacer{},
							},
						},
					},
				},
				VSpacer{},
				Composite{
					Layout: HBox{MarginsZero: true, Spacing: 10},
					Children: []Widget{
						HSpacer{},
						PushButton{
							AssignTo: &acceptButton,
							Text:     "确定",
							MinSize:  Size{Width: 80},
							OnClicked: func() {
								name := strings.TrimSpace(nameEdit.Text())
								inputUrl := strings.TrimSpace(urlEdit.Text())
								if inputUrl == "" {
									walk.MsgBox(dlg, "输入错误", "订阅链接不能为空！", walk.MsgBoxIconError)
									return
								}
								u, parseErr := url.ParseRequestURI(inputUrl)
								if parseErr != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
									walk.MsgBox(dlg, "输入错误", "请输入有效的 HTTP/HTTPS 订阅链接", walk.MsgBoxIconError)
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

		defer dlg.Dispose()

		currentSubEditor = dlg
		dlg.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
			currentSubEditor = nil
			if e.Dashboard.window != nil && e.Dashboard.window.Visible() && getValidOwner() != nil {
				e.Dashboard.window.Show()
				e.Dashboard.window.SetFocus()
			}
		})

		dlg.Run()
		resCh <- result{outName, outUrl, outInterval, accepted}
	})

	res := <-resCh
	return res.name, res.url, res.interval, res.ok
}
