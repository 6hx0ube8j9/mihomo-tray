package ui

import (
	"fmt"
	"strings"

	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"

	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/netutil"
)

func (e *Engine) ShowProfileInfoEditor(title string, current domain.ProfileInfo) (domain.ProfileInfo, bool) {
	next := current
	isRemote := current.IsRemote

	minVal := 0.0
	initAutoUpdate := false
	displayInterval := 0
	if isRemote {
		minVal = 1.0
		initAutoUpdate = current.AutoUpdate && current.Interval > 0
		displayInterval = current.Interval
		if displayInterval <= 0 {
			displayInterval = domain.DefaultUpdateInterval
		}
	}

	var nameEdit, urlEdit *walk.LineEdit
	var autoUpdateCB *walk.CheckBox
	var intervalEdit *walk.NumberEdit
	var unitLabel *walk.Label

	formLabel := func(text string, enabled bool) Label {
		return Label{Text: text, Alignment: AlignHNearVCenter, Enabled: enabled}
	}

	res := e.RunModalEditor("editor_profile_info", EditorConfig{
		Title:         title,
		Width:         450,
		AcceptBtnText: "确定",
		Widgets: []Widget{
			Composite{
				Layout: Grid{Columns: 2, MarginsZero: true},
				Children: []Widget{
					formLabel("配置名称", true),
					LineEdit{AssignTo: &nameEdit, Text: current.Name},

					formLabel("订阅链接", isRemote),
					LineEdit{AssignTo: &urlEdit, Text: current.URL, Enabled: isRemote},

					formLabel("更新频率", isRemote),
					Composite{
						Layout:  HBox{MarginsZero: true, Spacing: 8},
						Enabled: isRemote,
						Children: []Widget{
							CheckBox{
								AssignTo: &autoUpdateCB,
								Text:     "自动更新",
								Checked:  initAutoUpdate,
								Enabled:  isRemote,
								OnCheckedChanged: func() {
									enabled := isRemote && autoUpdateCB != nil && autoUpdateCB.Checked()
									if intervalEdit != nil {
										intervalEdit.SetEnabled(enabled)
									}
									if unitLabel != nil {
										unitLabel.SetEnabled(enabled)
									}
								},
							},
							NumberEdit{
								AssignTo: &intervalEdit,
								Value:    float64(displayInterval),
								MinValue: minVal,
								MaxValue: float64(domain.MaxUpdateInterval),
								Decimals: 0,
								MinSize:  Size{Width: 64},
								Enabled:  isRemote && initAutoUpdate,
							},
							Label{
								AssignTo: &unitLabel,
								Text:     "天",
								Enabled:  isRemote && initAutoUpdate,
							},
							HSpacer{},
						},
					},
				},
			},
		},
		OnAccept: func(dlg *walk.Dialog) (bool, error) {
			inputName := strings.TrimSpace(nameEdit.Text())
			inputURL := strings.TrimSpace(urlEdit.Text())

			if inputName == "" {
				nameEdit.SetFocus()
				RunErrorDialog(dlg, "输入错误", "配置名称不能为空")
				return false, nil
			}
			if isRemote && !netutil.IsValidHTTPURL(inputURL) {
				urlEdit.SetFocus()
				RunErrorDialog(dlg, "输入错误", "请输入有效的 HTTP/HTTPS 订阅链接")
				return false, nil
			}

			next.Name = inputName
			if !isRemote {
				next.URL = ""
				next.AutoUpdate = false
				next.Interval = 0
				return true, nil
			}

			inputAutoUpdate := autoUpdateCB.Checked()
			inputInterval := int(intervalEdit.Value())
			if inputAutoUpdate && (inputInterval < 1 || inputInterval > domain.MaxUpdateInterval) {
				intervalEdit.SetFocus()
				RunErrorDialog(dlg, "输入错误", fmt.Sprintf("更新周期必须在 1 到 %d 天之间", domain.MaxUpdateInterval))
				return false, nil
			}

			next.URL = inputURL
			next.AutoUpdate = inputAutoUpdate
			next.Interval = inputInterval
			return true, nil
		},
	})

	return next, res.Accepted
}
