package ui

import (
	"errors"
	"strconv"

	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"
	"mihomo-tray/internal/netutil"
)

var currentPortEditor *walk.Dialog

func (e *Engine) ShowPortEditor(defaultMixed, defaultSocks, defaultHttp int) (int, int, int, bool) {
	if e.app == nil || e.mw == nil {
		return 0, 0, 0, false
	}

	var mixedEdit, socksEdit, httpEdit *walk.LineEdit
	var finalMixed, finalSocks, finalHttp int

	formatErrorMsg := func(name string, err error) string {
		switch {
		case errors.Is(err, netutil.ErrEmptyPort):
			return name + " 端口不能为空，若禁用请填 0。"
		case errors.Is(err, netutil.ErrOutOfRange):
			return name + " 端口必须在 0 - 65535 之间。"
		case errors.Is(err, netutil.ErrInvalidType):
			return name + " 端口必须是纯数字。"
		default:
			return name + " 端口格式错误。"
		}
	}

	resultCh := make(chan EditorResult, 1)

	e.app.Synchronize(func() {
		res := RunEditor(e.mw, EditorConfig{
			AssignTo:  &currentPortEditor,
			Title:     "更改代理端口",
			Width:     320,
			MinHeight: 140,
			Widgets: []Widget{
				Composite{
					Layout: Grid{Columns: 2, Spacing: 10, MarginsZero: true},
					Children: []Widget{
						Label{Text: "Mixed 端口:", Alignment: AlignHFarVCenter},
						LineEdit{AssignTo: &mixedEdit, Text: strconv.Itoa(defaultMixed)},
						Label{Text: "Socks 端口:", Alignment: AlignHFarVCenter},
						LineEdit{AssignTo: &socksEdit, Text: strconv.Itoa(defaultSocks)},
						Label{Text: "HTTP(S) 端口:", Alignment: AlignHFarVCenter},
						LineEdit{AssignTo: &httpEdit, Text: strconv.Itoa(defaultHttp)},
					},
				},
			},
			OnAccept: func() (bool, error) {
				m, err := netutil.ParseAndValidatePort(mixedEdit.Text())
				if err != nil {
					RunErrorDialog(currentPortEditor, "输入错误", formatErrorMsg("Mixed", err))
					return false, nil
				}
				s, err := netutil.ParseAndValidatePort(socksEdit.Text())
				if err != nil {
					RunErrorDialog(currentPortEditor, "输入错误", formatErrorMsg("Socks", err))
					return false, nil
				}
				h, err := netutil.ParseAndValidatePort(httpEdit.Text())
				if err != nil {
					RunErrorDialog(currentPortEditor, "输入错误", formatErrorMsg("HTTP(S)", err))
					return false, nil
				}

				finalMixed, finalSocks, finalHttp = m, s, h
				return true, nil
			},
		})
		resultCh <- res
	})

	select {
	case res := <-resultCh:
		return finalMixed, finalSocks, finalHttp, res.Accepted
	case <-e.ctx.Done():
		return 0, 0, 0, false
	}
}
