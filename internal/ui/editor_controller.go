package ui

import (
	"strings"

	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"

	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/netutil"
	"mihomo-tray/internal/random"
)

func (e *Engine) ShowControllerEditor(defaultAddr, defaultSecret string, defaultOnline, defaultSysBrowser bool, defaultRemoteURL string) (string, string, bool, bool, string, bool) {
	if e.modalMgr == nil {
		return "", "", false, false, "", false
	}

	var finalAddr, finalSecret, finalRemoteURL string
	var finalOnline, finalSysBrowser bool
	var isAccepted bool

	e.RunOnUI(func() {
		key := "editor_controller"
		if !e.modalMgr.TryAcquire(key) {
			return
		}
		defer e.modalMgr.Release(key)

		var addrEdit, secretEdit, remoteURLEdit *walk.LineEdit
		var onlineCheck, sysBrowserCheck *walk.CheckBox
		var currentDlg *walk.Dialog

		toolBtn := func(text string, onClick func()) PushButton {
			return PushButton{
				Text:      text,
				MinSize:   Size{Width: 44},
				MaxSize:   Size{Width: 44},
				OnClicked: onClick,
			}
		}

		copyBtn := func(edit **walk.LineEdit) PushButton {
			return toolBtn("复制", func() {
				if *edit != nil {
					_ = walk.Clipboard().SetText((*edit).Text())
				}
			})
		}

		res := RunEditor(e.activeOwner(), EditorConfig{
			Title:         "Web 面板设置",
			Width:         430,
			MinHeight:     240,
			AcceptBtnText: "确定",
			OnReady: func(dlg *walk.Dialog) {
				currentDlg = dlg
				e.modalMgr.RegisterHWND(key, dlg.Handle())
			},
			Widgets: []Widget{
				GroupBox{
					Title:  "控制器连接",
					Layout: Grid{Columns: 2, Spacing: 6, Margins: Margins{Left: 8, Top: 10, Right: 8, Bottom: 8}},
					Children: []Widget{
						Label{
							Text:      "监听地址:",
							Alignment: AlignHFarVCenter,
						},
						Composite{
							Layout: HBox{MarginsZero: true, Spacing: 4},
							Children: []Widget{
								LineEdit{AssignTo: &addrEdit, Text: defaultAddr},
								copyBtn(&addrEdit),
								toolBtn("默认", func() { addrEdit.SetText(domain.DefaultExternalController) }),
							},
						},
						Label{
							Text:      "访问密钥:",
							Alignment: AlignHFarVCenter,
						},
						Composite{
							Layout: HBox{MarginsZero: true, Spacing: 4},
							Children: []Widget{
								LineEdit{AssignTo: &secretEdit, Text: defaultSecret},
								copyBtn(&secretEdit),
								toolBtn("生成", func() { secretEdit.SetText(random.String(domain.DefaultSecretLength)) }),
							},
						},
					},
				},
				GroupBox{
					Title:  "启动偏好",
					Layout: VBox{Margins: Margins{Left: 8, Top: 10, Right: 8, Bottom: 8}, Spacing: 8},
					Children: []Widget{
						Composite{
							Layout: HBox{MarginsZero: true, Spacing: 12},
							Children: []Widget{
								CheckBox{AssignTo: &onlineCheck, Text: "使用在线 Web 面板", Checked: defaultOnline},
								CheckBox{AssignTo: &sysBrowserCheck, Text: "使用系统默认浏览器", Checked: defaultSysBrowser},
								HSpacer{},
							},
						},
						Composite{
							Layout: HBox{MarginsZero: true, Spacing: 4},
							Children: []Widget{
								Label{
									Text:      "在线面板地址:",
									Alignment: AlignHNearVCenter,
								},
								LineEdit{AssignTo: &remoteURLEdit, Text: defaultRemoteURL},
								copyBtn(&remoteURLEdit),
								toolBtn("默认", func() { remoteURLEdit.SetText(domain.DefaultRemoteWebUIURL) }),
							},
						},
					},
				},
			},
			OnAccept: func() (bool, error) {
				addr := strings.TrimSpace(addrEdit.Text())
				secret := strings.TrimSpace(secretEdit.Text())
				rURL := strings.TrimSpace(remoteURLEdit.Text())

				showError := func(msg string) (bool, error) {
					RunErrorDialog(currentDlg, "保存失败", msg)
					return false, nil
				}

				switch {
				case !netutil.IsValidHostPort(addr):
					return showError("地址格式错误。")
				case netutil.IsPublicAddress(addr) && secret == "":
					return showError("当前地址支持外网访问，密钥不能为空。")
				case !netutil.IsValidHTTPURL(rURL):
					return showError("地址格式错误，请输入有效的 HTTP/HTTPS 链接。")
				}

				finalAddr = addr
				finalSecret = secret
				finalRemoteURL = rURL
				finalOnline = onlineCheck.Checked()
				finalSysBrowser = sysBrowserCheck.Checked()
				return true, nil
			},
		})

		isAccepted = res.Accepted
	})

	return finalAddr, finalSecret, finalOnline, finalSysBrowser, finalRemoteURL, isAccepted
}
