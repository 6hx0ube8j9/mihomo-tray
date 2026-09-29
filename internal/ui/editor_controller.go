package ui

import (
	"net"
	"strconv"
	"strings"

	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"

	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/netutil"
	"mihomo-tray/internal/random"
)

var currentControllerEditor *walk.Dialog

func (e *Engine) ShowControllerEditor(defaultAddr, defaultSecret string, defaultOnline, defaultSysBrowser bool) (string, string, bool, bool, bool) {
	var addrEdit, secretEdit *walk.LineEdit
	var onlineCheck, sysBrowserCheck *walk.CheckBox
	var finalAddr, finalSecret string
	var finalOnline, finalSysBrowser bool

	res := RunEditor(getValidOwner(), EditorConfig{
		AssignTo:      &currentControllerEditor,
		Title:         "Web 面板设置",
		Width:         460,
		MinHeight:     235,
		AcceptBtnText: "确定",
		Widgets: []Widget{
			Composite{
				Layout: VBox{MarginsZero: true, Spacing: 10},
				Children: []Widget{
					GroupBox{
						Title:  "控制器连接",
						Layout: Grid{Columns: 2, Spacing: 8, Margins: Margins{Left: 10, Top: 15, Right: 10, Bottom: 10}},
						Children: []Widget{
							Label{Text: "监听地址:", Alignment: AlignHFarVCenter},
							Composite{
								Layout: HBox{MarginsZero: true, Spacing: 4},
								Children: []Widget{
									LineEdit{AssignTo: &addrEdit, Text: defaultAddr},
									PushButton{
										Text:    "复制",
										MinSize: Size{Width: 44}, MaxSize: Size{Width: 44},
										OnClicked: func() {
											if err := walk.Clipboard().SetText(addrEdit.Text()); err == nil {
												ShowTrayNotification("提示", "监听地址已复制到剪贴板")
											}
										},
									},
									PushButton{
										Text:    "默认",
										MinSize: Size{Width: 44}, MaxSize: Size{Width: 44},
										OnClicked: func() {
											addrEdit.SetText(domain.DefaultExternalController)
										},
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
										OnClicked: func() {
											if err := walk.Clipboard().SetText(secretEdit.Text()); err == nil {
												ShowTrayNotification("提示", "访问密钥已复制到剪贴板")
											}
										},
									},
									PushButton{
										Text:    "生成",
										MinSize: Size{Width: 44}, MaxSize: Size{Width: 44},
										OnClicked: func() {
											secretEdit.SetText(random.String(domain.DefaultSecretLength))
										},
									},
								},
							},
						},
					},

					GroupBox{
						Title:  "启动偏好",
						Layout: HBox{Margins: Margins{Left: 10, Top: 15, Right: 10, Bottom: 10}, Spacing: 15},
						Children: []Widget{
							CheckBox{
								AssignTo: &onlineCheck,
								Text:     "使用在线 Web 面板",
								Checked:  defaultOnline,
							},
							CheckBox{
								AssignTo: &sysBrowserCheck,
								Text:     "使用系统默认浏览器打开",
								Checked:  defaultSysBrowser,
							},
							HSpacer{},
						},
					},

					VSpacer{},
				},
			},
		},
		OnAccept: func() (bool, error) {
			addr := strings.TrimSpace(addrEdit.Text())
			secret := strings.TrimSpace(secretEdit.Text())

			if addr == "" {
				RunErrorDialog(currentControllerEditor, "保存失败", "监听地址不能为空。")
				return false, nil
			}

			_, portStr, err := net.SplitHostPort(addr)
			isFormatValid := err == nil && !strings.ContainsAny(addr, " \t\r\n")
			if isFormatValid {
				port, pErr := strconv.Atoi(portStr)
				isFormatValid = pErr == nil && port > 0 && port <= 65535
			}

			if !isFormatValid {
				RunErrorDialog(currentControllerEditor, "保存失败", "输入格式错误，端口须在 1-65535 之间。")
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

	return finalAddr, finalSecret, finalOnline, finalSysBrowser, res.Accepted
}
