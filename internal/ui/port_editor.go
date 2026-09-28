package ui

import (
	"strconv"
	"strings"

	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"
	"github.com/tailscale/win"
)

var currentPortEditor *walk.Dialog

func (e *Engine) ShowPortEditor(defaultMixed, defaultSocks, defaultHttp int) (int, int, int, bool) {
	if e.app == nil || e.mw == nil {
		return 0, 0, 0, false
	}

	type result struct {
		mixed, socks, http int
		ok                 bool
	}
	resCh := make(chan result)

	e.app.Synchronize(func() {
		if currentPortEditor != nil {
			hwnd := currentPortEditor.Handle()
			if win.IsIconic(hwnd) {
				win.ShowWindow(hwnd, win.SW_RESTORE)
			}
			win.SetForegroundWindow(hwnd)
			currentPortEditor.SetFocus()
			resCh <- result{ok: false}
			return
		}

		var dlg *walk.Dialog
		var mixedEdit, socksEdit, httpEdit *walk.LineEdit
		var acceptButton, cancelButton *walk.PushButton
		var outMixed, outSocks, outHttp int
		var accepted bool
		
		owner := getValidOwner()
		hActive := win.GetForegroundWindow()

		err := Dialog{
			AssignTo:      &dlg,
			Title:         "更改代理端口",
			MinSize:       Size{Width: 320, Height: 200},
			Layout:        VBox{Margins: Margins{Left: 20, Top: 20, Right: 20, Bottom: 15}, Spacing: 15},
			DefaultButton: &acceptButton,
			CancelButton:  &cancelButton,
			Children: []Widget{
				Composite{
					Layout: Grid{Columns: 2, Spacing: 10, MarginsZero: true},
					Children: []Widget{
						Label{Text: "Mixed 端口:"},
						LineEdit{AssignTo: &mixedEdit, Text: strconv.Itoa(defaultMixed)},
						Label{Text: "Socks 端口:"},
						LineEdit{AssignTo: &socksEdit, Text: strconv.Itoa(defaultSocks)},
						Label{Text: "HTTP(S) 端口:"},
						LineEdit{AssignTo: &httpEdit, Text: strconv.Itoa(defaultHttp)},
					},
				},
				VSpacer{},
				Composite{
					Layout: HBox{MarginsZero: true, Spacing: 10},
					Children: []Widget{
						HSpacer{},
						PushButton{
							AssignTo:  &cancelButton,
							Text:      "取消",
							MinSize:   Size{Width: 80, Height: 26},
							OnClicked: func() { dlg.Cancel() },
						},
						PushButton{
							AssignTo: &acceptButton,
							Text:     "保存",
							MinSize:  Size{Width: 80, Height: 26},
							OnClicked: func() {
								parsePort := func(s string, name string) (int, bool) {
									s = strings.TrimSpace(s)
									if s == "" {
										RunErrorDialog(dlg, "输入错误", name+" 端口不能为空，若禁用请填 0")
										return 0, false
									}
									port, err := strconv.Atoi(s)
									if err != nil || port < 0 || port > 65535 {
										RunErrorDialog(dlg, "输入错误", name+" 端口必须是 0 - 65535 之间的有效数字")
										return 0, false
									}
									return port, true
								}

								mixed, ok := parsePort(mixedEdit.Text(), "Mixed")
								if !ok {
									return
								}
								socks, ok := parsePort(socksEdit.Text(), "Socks")
								if !ok {
									return
								}
								http, ok := parsePort(httpEdit.Text(), "HTTP(S)")
								if !ok {
									return
								}

								outMixed = mixed
								outSocks = socks
								outHttp = http
								accepted = true
								dlg.Accept()
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
			currentPortEditor = dlg

			dlg.Starting().Attach(func() {
				centerDialog(dlg, owner, hActive)
			})

			dlg.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
				currentPortEditor = nil
				if e.Dashboard != nil && e.Dashboard.window != nil && e.Dashboard.window.Visible() {
					hwnd := e.Dashboard.window.Handle()
					if win.IsIconic(hwnd) {
						win.ShowWindow(hwnd, win.SW_RESTORE)
					}
					win.SetForegroundWindow(hwnd)
					e.Dashboard.window.SetFocus()
				} else if hActive != 0 && win.IsWindowVisible(hActive) && !win.IsIconic(hActive) {
					win.SetForegroundWindow(hActive)
					win.SetFocus(hActive)
				}
			})

			dlg.Run()
			resCh <- result{outMixed, outSocks, outHttp, accepted}
		})

		res := <-resCh
		return res.mixed, res.socks, res.http, res.ok
	}
}
