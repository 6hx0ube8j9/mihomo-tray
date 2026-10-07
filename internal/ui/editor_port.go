package ui

import (
	"errors"
	"strconv"

	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"
	"mihomo-tray/internal/netutil"
)

func (e *Engine) ShowPortEditor(defaultMixed, defaultSocks, defaultHttp int) (int, int, int, bool) {
	if e.modalMgr == nil {
		return 0, 0, 0, false
	}

	var finalMixed, finalSocks, finalHttp int
	var isAccepted bool

	e.RunOnUI(func() {
		key := "editor_port"
		if !e.modalMgr.TryAcquire(key) {
			return
		}
		defer e.modalMgr.Release(key)

		var mixedEdit, socksEdit, httpEdit *walk.LineEdit
		var currentDlg *walk.Dialog

		portLabel := func(name string) Label {
			return Label{Text: name + " 端口:", Alignment: AlignHFarVCenter}
		}

		res := RunEditor(e.activeOwner(), EditorConfig{
			Title:     "更改代理端口",
			Width:     320,
			MinHeight: 140,
			OnReady: func(dlg *walk.Dialog) {
				currentDlg = dlg
				e.modalMgr.RegisterHWND(key, dlg.Handle())
			},
			Widgets: []Widget{
				Composite{
					Layout: Grid{Columns: 2, Spacing: 10, MarginsZero: true},
					Children: []Widget{
						portLabel("Mixed"),
						LineEdit{AssignTo: &mixedEdit, Text: strconv.Itoa(defaultMixed)},

						portLabel("Socks"),
						LineEdit{AssignTo: &socksEdit, Text: strconv.Itoa(defaultSocks)},

						portLabel("HTTP(S)"),
						LineEdit{AssignTo: &httpEdit, Text: strconv.Itoa(defaultHttp)},
					},
				},
			},
			OnAccept: func() (bool, error) {
				validate := func(name string, edit *walk.LineEdit) (int, bool) {
					val, err := netutil.ParseAndValidatePort(edit.Text())
					if err != nil {
						RunErrorDialog(currentDlg, "输入错误", formatPortError(name, err))
						return 0, false
					}
					return val, true
				}

				m, ok := validate("Mixed", mixedEdit)
				if !ok {
					return false, nil
				}
				s, ok := validate("Socks", socksEdit)
				if !ok {
					return false, nil
				}
				h, ok := validate("HTTP(S)", httpEdit)
				if !ok {
					return false, nil
				}

				finalMixed, finalSocks, finalHttp = m, s, h
				return true, nil
			},
		})

		isAccepted = res.Accepted
	})

	return finalMixed, finalSocks, finalHttp, isAccepted
}

func formatPortError(name string, err error) string {
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
