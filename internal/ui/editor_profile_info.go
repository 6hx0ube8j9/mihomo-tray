package ui

import (
	"strings"

	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"

	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/netutil"
)

var currentProfileInfoEditor *walk.Dialog

func (e *Engine) ShowProfileInfoEditor(title, defaultName, defaultUrl string, defaultInterval int, isRemote bool) (string, string, int, bool) {
	if e.app == nil || e.mw == nil {
		return "", "", 0, false
	}

	var nameEdit, urlEdit *walk.LineEdit
	var intervalEdit *walk.NumberEdit
	var finalName, finalUrl string
	var finalInterval int

	resultCh := make(chan EditorResult, 1)

	e.app.Synchronize(func() {
		res := RunEditor(e.activeOwner(), EditorConfig{
			AssignTo:      &currentProfileInfoEditor,
			Title:         title,
			Width:         450,
			MinHeight:     145,
			AcceptBtnText: "确定",
			Widgets: []Widget{
				Composite{
					Layout: Grid{Columns: 2, Spacing: 10, MarginsZero: true},
					Children: []Widget{
						Label{Text: "配置名称:", Alignment: AlignHFarVCenter},
						LineEdit{AssignTo: &nameEdit, Text: defaultName},

						Label{Text: "订阅链接:", Alignment: AlignHFarVCenter, Enabled: isRemote},
						LineEdit{AssignTo: &urlEdit, Text: defaultUrl, Enabled: isRemote},

						Label{Text: "更新频率:", Alignment: AlignHFarVCenter, Enabled: isRemote},
						Composite{
							Layout: HBox{MarginsZero: true},
							Enabled: isRemote,
							Children: []Widget{
								NumberEdit{AssignTo: &intervalEdit, Value: float64(defaultInterval), MinValue: 0, MaxValue: float64(domain.MaxUpdateInterval), Enabled: isRemote},
								Label{Text: "天 (填 0 为禁用自动更新)", Enabled: isRemote},
								HSpacer{},
							},
						},
					},
				},
			},
			OnAccept: func() (bool, error) {
				inputName := strings.TrimSpace(nameEdit.Text())
				inputUrl := strings.TrimSpace(urlEdit.Text())

				if isRemote {
					if !netutil.IsValidHTTPURL(inputUrl) {
						RunErrorDialog(currentProfileInfoEditor, "输入错误", "请输入有效的 HTTP/HTTPS 订阅链接")
						return false, nil
					}
				}

				finalName = inputName
				finalUrl = inputUrl
				finalInterval = int(intervalEdit.Value())
				return true, nil
			},
		})
		resultCh <- res
	})

	select {
	case res := <-resultCh:
		return finalName, finalUrl, finalInterval, res.Accepted
	case <-e.ctx.Done():
		return "", "", 0, false
	}
}
