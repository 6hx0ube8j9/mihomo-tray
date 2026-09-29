package ui

import (
	"crypto/rand"
	"encoding/hex"
	"strings"

	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"
	"github.com/tailscale/win"
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
		Width:     460,
		MinHeight: 280,
		Widgets: []Widget{
			Composite{
				Layout: Grid{Columns: 3, MarginsZero: true, Spacing: 10},
				Children: []Widget{
					Label{Text: "外部监听地址:"},
					LineEdit{AssignTo: &addrEdit, Text: defaultAddr},
					PushButton{
						Text:    "复制",
						MinSize: Size{Width: 60},
						OnClicked: func() {
							if err := walk.Clipboard().SetText(addrEdit.Text()); err == nil {
								ShowTrayNotification("提示", "监听地址已复制到剪贴板")
							}
						},
					},

					Label{Text: "访问密钥:"},
					LineEdit{AssignTo: &secretEdit, Text: defaultSecret, PasswordMode: true},
					Composite{
						Layout: HBox{MarginsZero: true, Spacing: 5},
						Children: []Widget{
							PushButton{
								Text:    "生成",
								MinSize: Size{Width: 50},
								OnClicked: func() {
									b := make([]byte, 16)
									_, _ = rand.Read(b)
									secretEdit.SetText(hex.EncodeToString(b))
								},
							},
							PushButton{
								Text:    "复制",
								MinSize: Size{Width: 50},
								OnClicked: func() {
									if err := walk.Clipboard().SetText(secretEdit.Text()); err == nil {
										ShowTrayNotification("提示", "访问密钥已复制到剪贴板")
									}
								},
							},
						},
					},
				},
			},
			VSpacer{Size: 15},
			Composite{
				Layout: Grid{Columns: 2, MarginsZero: true, Spacing: 8},
				Children: []Widget{
					Label{Text: "使用在线 Web 面板:"},
					CheckBox{AssignTo: &onlineCheck, Checked: defaultOnline},

					Label{},
					Label{Text: "⚠️ 提示：通过第三方托管页面连接，可能存在配置泄漏风险", TextColor: walk.RGB(200, 80, 0)},

					Label{Text: "使用默认浏览器打开面板:"},
					CheckBox{AssignTo: &sysBrowserCheck, Checked: defaultSysBrowser},
				},
			},
		},
		OnAccept: func() (bool, error) {
			addr := strings.TrimSpace(addrEdit.Text())
			secret := strings.TrimSpace(secretEdit.Text())

			if addr == "" {
				RunErrorDialog(currentControllerEditor, "输入错误", "外部监听地址不能为空")
				return false, nil
			}

			isPublic := strings.HasPrefix(addr, "0.0.0.0") || strings.HasPrefix(addr, ":") || strings.HasPrefix(addr, "[::]")
			if isPublic {
				RunAlertDialog(currentControllerEditor, "安全警告", "监听地址已设为公开访问，建议设置较强访问密钥。", walk.IconWarning(), win.MB_ICONWARNING)
			}

			if secret == "" {
				if !RunConfirmDialog(currentControllerEditor, "安全提示", "当前未设置访问密钥，接口处于公开状态。确定保持空密码吗？") {
					return false, nil
				}
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
