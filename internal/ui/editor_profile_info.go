package ui

import (
	"strings"

	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"

	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/netutil"
)

func (e *Engine) ShowProfileInfoEditor(title, defaultName, defaultUrl string, defaultInterval int, isRemote bool) (string, string, int, bool) {
	if e.modalMgr == nil {
		return "", "", 0, false
	}

	var finalName, finalUrl string
	var finalInterval int
	var isAccepted bool

	e.RunOnUI(func() {
		key := "editor_profile_info"
		if !e.modalMgr.TryAcquire(key) {
			return
		}
		defer e.modalMgr.Release(key)

		var nameEdit, urlEdit *walk.LineEdit
		var intervalEdit *walk.NumberEdit
		var currentDlg *walk.Dialog

		formLabel := func(text string, enabled bool) Label {
			return Label{Text: text, Alignment: AlignHFarVCenter, Enabled: enabled}
		}

		res := RunEditor(e.activeOwner(), EditorConfig{
			Title:         title,
			Width:         450,
			MinHeight:     145,
			AcceptBtnText: "确定",
			OnReady: func(dlg *walk.Dialog) {
				currentDlg = dlg
				e.modalMgr.RegisterHWND(key, dlg.Handle())
			},
			Widgets: []Widget{
				Composite{
					Layout: Grid{Columns: 2, Spacing: 10, MarginsZero: true},
					Children: []Widget{
						formLabel("配置名称:", true),
						LineEdit{AssignTo: &nameEdit, Text: defaultName},

						formLabel("订阅链接:", isRemote),
						LineEdit{AssignTo: &urlEdit, Text: defaultUrl, Enabled: isRemote},

						formLabel("更新频率:", isRemote),
						Composite{
							Layout:  HBox{MarginsZero: true},
							Enabled: isRemote,
							Children: []Widget{
								NumberEdit{
									AssignTo: &intervalEdit,
									Value:    float64(defaultInterval),
									MinValue: 0,
									MaxValue: float64(domain.MaxUpdateInterval),
								},
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

				if inputName == "" {
					RunErrorDialog(currentDlg, "输入错误", "配置名称不能为空。")
					return false, nil
				}
				if isRemote && !netutil.IsValidHTTPURL(inputUrl) {
					RunErrorDialog(currentDlg, "输入错误", "请输入有效的 HTTP/HTTPS 订阅链接。")
					return false, nil
				}

				finalName = inputName
				finalUrl = inputUrl
				finalInterval = int(intervalEdit.Value())
				return true, nil
			},
		})

		isAccepted = res.Accepted
	})

	return finalName, finalUrl, finalInterval, isAccepted
}
