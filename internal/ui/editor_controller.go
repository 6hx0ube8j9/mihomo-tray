package ui

import (
	"strings"

	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"

	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/netutil"
	"mihomo-tray/internal/random"
)

var currentControllerEditor *walk.Dialog

func (e *Engine) ShowControllerEditor(defaultAddr, defaultSecret string, defaultOnline, defaultSysBrowser bool) (string, string, bool, bool, bool) {
	if e.app == nil || e.mw == nil {
		return "", "", false, false, false
	}

	var addrEdit, secretEdit *walk.LineEdit
	var onlineCheck, sysBrowserCheck *walk.CheckBox
	var finalAddr, finalSecret string
	var finalOnline, finalSysBrowser bool

	resultCh := make(chan EditorResult, 1)

	e.app.Synchronize(func() {
		res := RunEditor(e.mw, EditorConfig{
			AssignTo:      &currentControllerEditor,
			Title:         "Web 面板设置",
			Width:         415,
			MinHeight:     195,
			AcceptBtnText: "确定",
			Widgets: []Widget{
				Composite{
					Layout: VBox{MarginsZero: true, Spacing: 8},
					Children: []Widget{
						GroupBox{
							Title:  "控制器连接",
							Layout: Grid{Columns: 2, Spacing: 6, Margins: Margins{Left: 8, Top: 12, Right: 8, Bottom: 8}},
							Children: []Widget{
								Label{Text: "监听地址:", Alignment: AlignHFarVCenter},
								Composite{
									Layout: HBox{MarginsZero: true, Spacing: 4},
									Children: []Widget{
										LineEdit{AssignTo: &addrEdit, Text: defaultAddr},
										PushButton{
											Text:    "复制",
											MinSize: Size{Width: 44}, MaxSize: Size{Width: 44},
											OnClicked: func() { _ = walk.Clipboard().SetText(addrEdit.Text()) },
										},
										PushButton{
											Text:    "默认",
											MinSize: Size{Width: 44}, MaxSize: Size{Width: 44},
											OnClicked: func() { addrEdit.SetText(domain.DefaultExternalController) },
										},
									},
								},
								Label{Text: "访问密钥:", Alignment: AlignHFarVCenter},
								Composite{
									Layout: HBox{MarginsZero: true, Spacing: 4},
									Children: []Widget{
										LineEdit{AssignTo: &secretEdit, Text: defaultSecret},
										PushButton{
											Text:    "复制",
											MinSize: Size{Width: 44}, MaxSize: Size{Width: 44},
											OnClicked: func() { _ = walk.Clipboard().SetText(secretEdit.Text()) },
										},
										PushButton{
											Text:    "生成",
											MinSize: Size{Width: 44}, MaxSize: Size{Width: 44},
											OnClicked: func() { secretEdit.SetText(random.String(domain.DefaultSecretLength)) },
										},
									},
								},
							},
						},
						GroupBox{
							Title:  "启动偏好",
							Layout: HBox{Margins: Margins{Left: 8, Top: 10, Right: 8, Bottom: 8}, Spacing: 12},
							Children: []Widget{
								CheckBox{AssignTo: &onlineCheck, Text: "使用在线 Web 面板", Checked: defaultOnline},
								CheckBox{AssignTo: &sysBrowserCheck, Text: "使用系统默认浏览器", Checked: defaultSysBrowser},
								HSpacer{},
							},
						},
					},
				},
			},
			OnAccept: func() (bool, error) {
				addr := strings.TrimSpace(addrEdit.Text())
				secret := strings.TrimSpace(secretEdit.Text())

				if !netutil.IsValidHostPort(addr) {
					RunErrorDialog(currentControllerEditor, "保存失败", "地址格式错误。")
					return false, nil
				}
				if netutil.IsPublicAddress(addr) && secret == "" {
					RunErrorDialog(currentControllerEditor, "保存失败", "当前地址支持外网访问，密钥不能为空。")
					return false, nil
				}

				finalAddr = addr
				finalSecret = secret
				finalOnline = onlineCheck.Checked()
				finalSysBrowser = sysBrowserCheck.Checked()
				return true, nil
			},
		})
		resultCh <- res
	})

	select {
	case res := <-resultCh:
		return finalAddr, finalSecret, finalOnline, finalSysBrowser, res.Accepted
	case <-e.ctx.Done():
		return "", "", false, false, false
	}
}
