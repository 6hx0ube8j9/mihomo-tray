package ui

import (
	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"
)

func (e *Engine) ShowPortEditor(defaultMixed, defaultSocks, defaultHttp int) (int, int, int, bool) {
	var finalMixed, finalSocks, finalHttp int

	var mixedEdit, socksEdit, httpEdit *walk.NumberEdit

	portLabel := func(name string) Label {
		return Label{Text: name + " 端口:", Alignment: AlignHFarVCenter}
	}

	portNumberEdit := func(assign **walk.NumberEdit, val int) NumberEdit {
		return NumberEdit{
			AssignTo: assign,
			Value:    float64(val),
			MinValue: 0,
			MaxValue: 65535,
			Decimals: 0,
		}
	}

	res := e.RunModalEditor("editor_port", EditorConfig{
		Title:     "更改代理端口",
		Width:     260,
		MinHeight: 120,
		Widgets: []Widget{
			Composite{
				Layout: Grid{Columns: 2, Spacing: 10, MarginsZero: true},
				Children: []Widget{
					portLabel("Mixed"),
					portNumberEdit(&mixedEdit, defaultMixed),

					portLabel("Socks"),
					portNumberEdit(&socksEdit, defaultSocks),

					portLabel("HTTP(S)"),
					portNumberEdit(&httpEdit, defaultHttp),
				},
			},
		},
		OnAccept: func() (bool, error) {
			// 直接物理级防呆取值，零错误处理
			finalMixed = int(mixedEdit.Value())
			finalSocks = int(socksEdit.Value())
			finalHttp = int(httpEdit.Value())
			return true, nil
		},
	})

	return finalMixed, finalSocks, finalHttp, res.Accepted
}
