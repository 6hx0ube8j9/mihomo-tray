package ui

import (
	"strconv"
	"strings"

	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"
)

var currentPortEditor *walk.Dialog

func (e *Engine) ShowPortEditor(defaultMixed, defaultSocks, defaultHttp int) (int, int, int, bool) {
	var mixedEdit, socksEdit, httpEdit *walk.LineEdit
	var finalMixed, finalSocks, finalHttp int

	parsePort := func(text string, name string) (int, bool) {
		text = strings.TrimSpace(text)
		if text == "" {
			RunErrorDialog(currentPortEditor, "输入错误", name+" 端口不能为空，若禁用请填 0")
			return 0, false
		}
		port, err := strconv.Atoi(text)
		if err != nil || port < 0 || port > 65535 {
			RunErrorDialog(currentPortEditor, "输入错误", name+" 端口必须是 0 - 65535 之间的有效数字")
			return 0, false
		}
		return port, true
	}

	res := RunEditor(getValidOwner(), EditorConfig{
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
			m, ok := parsePort(mixedEdit.Text(), "Mixed")
			if !ok {
				return false, nil
			}
			s, ok := parsePort(socksEdit.Text(), "Socks")
			if !ok {
				return false, nil
			}
			h, ok := parsePort(httpEdit.Text(), "HTTP(S)")
			if !ok {
				return false, nil
			}

			finalMixed = m
			finalSocks = s
			finalHttp = h
			return true, nil
		},
	})

	return finalMixed, finalSocks, finalHttp, res.Accepted
}
