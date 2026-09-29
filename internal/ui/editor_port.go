package ui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"
	"github.com/tailscale/win"
)

var currentPortEditor *walk.Dialog

func (e *Engine) ShowPortEditor(defaultMixed, defaultSocks, defaultHttp int) (int, int, int, bool) {
	if currentPortEditor != nil {
		e.app.Synchronize(func() {
			if currentPortEditor.Visible() {
				win.SetForegroundWindow(currentPortEditor.Handle())
				currentPortEditor.SetFocus()
			}
		})
		return 0, 0, 0, false
	}

	var mixedEdit, socksEdit, httpEdit *walk.LineEdit
	
	parsePort := func(text string, defVal int) (int, error) {
		text = strings.TrimSpace(text)
		if text == "" {
			return defVal, nil
		}
		port, err := strconv.Atoi(text)
		if err != nil || port <= 0 || port > 65535 {
			return 0, fmt.Errorf("无效的端口号: %s", text)
		}
		return port, nil
	}

	var finalMixed, finalSocks, finalHttp int

	res := RunEditor(getValidOwner(), EditorConfig{
		AssignTo: &currentPortEditor,
		Title:    "更改代理端口",
		Width:    320,
		Widgets: []Widget{
			Composite{
				Layout: VBox{MarginsZero: true, Spacing: 8},
				Children: []Widget{
					Label{Text: "Mixed 端口 (默认: 7890):"},
					LineEdit{AssignTo: &mixedEdit, Text: fmt.Sprintf("%d", defaultMixed)},
					Label{Text: "Socks 端口 (默认: 7891):"},
					LineEdit{AssignTo: &socksEdit, Text: fmt.Sprintf("%d", defaultSocks)},
					Label{Text: "HTTP 端口 (默认: 7892):"},
					LineEdit{AssignTo: &httpEdit, Text: fmt.Sprintf("%d", defaultHttp)},
				},
			},
		},
		OnAccept: func() (bool, error) {
			m, err := parsePort(mixedEdit.Text(), defaultMixed)
			if err != nil {
				RunErrorDialog(nil, "输入错误", err.Error())
				return false, nil
			}
			s, err := parsePort(socksEdit.Text(), defaultSocks)
			if err != nil {
				RunErrorDialog(nil, "输入错误", err.Error())
				return false, nil
			}
			h, err := parsePort(httpEdit.Text(), defaultHttp)
			if err != nil {
				RunErrorDialog(nil, "输入错误", err.Error())
				return false, nil
			}

			finalMixed = m
			finalSocks = s
			finalHttp = h
			return true, nil
		},
	})

	currentPortEditor = nil
	return finalMixed, finalSocks, finalHttp, res.Accepted
}
