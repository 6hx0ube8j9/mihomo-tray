package ui

import (
	"fmt"
	"strings"

	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"
)

type intervalOption struct {
	Name  string
	Value int
}

var currentSubEditor *walk.Dialog

func (e *Engine) ShowSubscriptionEditor(title, defaultName, defaultURL string, defaultInterval int) (string, string, int, bool) {
	if currentSubEditor != nil {
		e.app.Synchronize(func() {
			if currentSubEditor.Visible() {
				walk.App().ActiveForm().SetFocus()
			}
		})
		return "", "", 0, false
	}

	var nameEdit, urlEdit *walk.LineEdit
	var intervalCombo *walk.ComboBox

	options := []*intervalOption{
		{"不自动更新", 0},
		{"每 1 小时", 60},
		{"每 6 小时", 360},
		{"每 12 小时", 720},
		{"每 24 小时", 1440},
		{"每 48 小时", 2880},
		{"每 72 小时", 4320},
	}

	// 找出默认选中项的索引
	defaultIndex := 0
	for i, opt := range options {
		if opt.Value == defaultInterval {
			defaultIndex = i
			break
		}
	}

	var finalName, finalURL string
	var finalInterval int

	res := RunEditor(getValidOwner(), EditorConfig{
		AssignTo: &currentSubEditor,
		Title: title,
		Width: 380,
		Widgets: []Widget{
			Composite{
				Layout: VBox{MarginsZero: true, Spacing: 8},
				Children: []Widget{
					Label{Text: "配置名称 (可选，留空则自动生成):"},
					LineEdit{
						AssignTo: &nameEdit,
						Text:     defaultName,
					},
					VSpacer{Size: 4},
					Label{Text: "订阅链接 (必填):"},
					LineEdit{
						AssignTo: &urlEdit,
						Text:     defaultURL,
					},
					VSpacer{Size: 4},
					Label{Text: "自动更新频率:"},
					ComboBox{
						AssignTo:      &intervalCombo,
						Value:         defaultIndex,
						BindingMember: "Value",
						DisplayMember: "Name",
						Model:         options,
					},
				},
			},
		},
		OnAccept: func() (bool, error) {
			inputName := strings.TrimSpace(nameEdit.Text())
			inputURL := strings.TrimSpace(urlEdit.Text())

			if inputURL == "" {
				RunErrorDialog(nil, "输入错误", "订阅链接不能为空。")
				return false, nil
			}

			if !strings.HasPrefix(inputURL, "http://") && !strings.HasPrefix(inputURL, "https://") {
				RunErrorDialog(nil, "输入错误", "订阅链接格式不正确，必须以 http:// 或 https:// 开头。")
				return false, nil
			}

			idx := intervalCombo.CurrentIndex()
			selectedInterval := 0
			if idx >= 0 && idx < len(options) {
				selectedInterval = options[idx].Value
			}

			finalName = inputName
			finalURL = inputURL
			finalInterval = selectedInterval

			return true, nil
		},
	})
	currentSubEditor = nil

	return finalName, finalURL, finalInterval, res.Accepted
}
