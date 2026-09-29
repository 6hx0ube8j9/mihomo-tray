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
		AssignTo:  &currentControllerEditor,
		Title:     "Web 面板设置",
		Width:     420,
		MinHeight: 180,
		Widgets: []Widget{
			Composite{
				Layout: Grid{Columns: 2, MarginsZero: true, Spacing: 10},
				Children: []Widget{
					Label{Text: "外部监听地址:"},
					Composite{
						Layout: HBox{MarginsZero: true, Spacing: 5},
						Children: []Widget{
							LineEdit{AssignTo: &addrEdit, Text: defaultAddr},
							PushButton{
								Text:    "复制",
								MinSize: Size{Width: 50},
								OnClicked: func() {
									if err := walk.Clipboard().SetText(addrEdit.Text()); err == nil {
										ShowTrayNotification("提示", "监听地址已复制到剪贴板")
									}
								},
							},
							PushButton{
								Text:    "默认",
								MinSize: Size{Width: 50},
								OnClicked: func() {
									addrEdit.SetText(domain.DefaultExternalController)
								},
							},
						},
					},

					Label{Text: "访问密钥:"},
					Composite{
						Layout: HBox{MarginsZero: true, Spacing: 5},
						Children: []Widget{
							LineEdit{AssignTo: &secretEdit, Text: defaultSecret},
							PushButton{
								Text:    "复制",
								MinSize: Size{Width: 50},
								OnClicked: func() {
									if err := walk.Clipboard().SetText(secretEdit.Text()); err == nil {
										ShowTrayNotification("提示", "访问密钥已复制到剪贴板")
									}
								},
							},
							PushButton{
								Text:    "生成",
								MinSize: Size{Width: 50},
								OnClicked: func() {
									secretEdit.SetText(random.String(domain.DefaultSecretLength))
								},
							},
						},
					},

					VSpacer{Size: 5},
					Label{},

					Label{Text: "使用在线 Web 面板:"},
					CheckBox{AssignTo: &onlineCheck, Checked: defaultOnline},

					Label{Text: "使用默认浏览器打开:"},
					CheckBox{AssignTo: &sysBrowserCheck, Checked: defaultSysBrowser},
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
				RunErrorDialog(currentControllerEditor, "保存失败", "输入格式错误。")
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
