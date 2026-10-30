package ui

import (
	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"
)

func (e *Engine) ShowPortEditor(defaultMixed, defaultSocks, defaultHttp int) (int, int, int, bool) {
	var finalMixed, finalSocks, finalHttp int

	var mixedEdit, socksEdit, httpEdit *walk.NumberEdit
	var currentDlg *walk.Dialog

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
		OnReady: func(dlg *walk.Dialog) {
			currentDlg = dlg
		},
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
			mVal := int(mixedEdit.Value())
			sVal := int(socksEdit.Value())
			hVal := int(httpEdit.Value())

			hasCollision := false
			if mVal > 0 && (mVal == sVal || mVal == hVal) {
				hasCollision = true
			}
			if sVal > 0 && sVal == hVal {
				hasCollision = true
			}

			if hasCollision {
				RunErrorDialog(currentDlg, "端口冲突", "代理端口不能重复 (0 为禁用)")
				return false, nil
			}

			finalMixed = mVal
			finalSocks = sVal
			finalHttp = hVal
			return true, nil
		},
	})

	return finalMixed, finalSocks, finalHttp, res.Accepted
}
