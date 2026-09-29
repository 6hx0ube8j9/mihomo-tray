package ui

import (
	"net/url"
	"strings"

	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"

	"mihomo-tray/internal/domain"
)

var currentSubEditor *walk.Dialog

func (e *Engine) ShowSubscriptionEditor(title, defaultName, defaultUrl string, defaultInterval int) (string, string, int, bool) {
	var nameEdit, urlEdit *walk.LineEdit
	var intervalEdit *walk.NumberEdit
	var finalName, finalUrl string
	var finalInterval int

	res := RunEditor(getValidOwner(), EditorConfig{
		AssignTo:      &currentSubEditor,
		Title:         title,
		Width:         450,
		MinHeight:     200,
		AcceptBtnText: "确定",
		Widgets: []Widget{
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
		},
		OnAccept: func() (bool, error) {
			inputName := strings.TrimSpace(nameEdit.Text())
			inputUrl := strings.TrimSpace(urlEdit.Text())
			
			if inputUrl == "" {
				RunErrorDialog(currentSubEditor, "输入错误", "订阅链接不能为空！")
				return false, nil
			}
			u, parseErr := url.ParseRequestURI(inputUrl)
			if parseErr != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				RunErrorDialog(currentSubEditor, "输入错误", "请输入有效的 HTTP/HTTPS 订阅链接")
				return false, nil
			}

			finalName = inputName
			finalUrl = inputUrl
			finalInterval = int(intervalEdit.Value())
			return true, nil
		},
	})

	return finalName, finalUrl, finalInterval, res.Accepted
}
