package ui

import (
	"strings"

	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"
	"github.com/tailscale/win"

	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/netutil"
	"mihomo-tray/internal/random"
)

type webOpenModeOption struct {
	Value string
	Label string
}

var webOpenModeOptions = []webOpenModeOption{
	{Value: domain.WebOpenModeAuto, Label: "自动选择 (Auto)"},
	{Value: domain.WebOpenModeApp, Label: "应用模式 (App)"},
	{Value: domain.WebOpenModeBrowser, Label: "浏览器 (Browser)"},
	{Value: domain.WebOpenModeWebView, Label: "内置窗口 (WebView)"},
}

func webOpenModeLabels() []string {
	labels := make([]string, len(webOpenModeOptions))
	for i, opt := range webOpenModeOptions {
		labels[i] = opt.Label
	}
	return labels
}

func findWebOpenModeIndex(mode string) int {
	normalized := domain.NormalizeWebOpenMode(mode)
	for i, opt := range webOpenModeOptions {
		if opt.Value == normalized {
			return i
		}
	}
	return 0
}

func (e *Engine) ShowControllerEditor(current domain.ControllerSettings) (domain.ControllerSettings, bool) {
	result := current
	initialMode := domain.NormalizeWebOpenMode(current.WebOpenMode)

	var addrEdit, secretEdit, remoteURLEdit, pathEdit *walk.LineEdit
	var onlineCheck, customBrowserCheck *walk.CheckBox
	var modeCombo *walk.ComboBox

	isInitializing := true

	smallBtn := func(text string, onClick func()) PushButton {
		return PushButton{
			Text:      text,
			MinSize:   Size{Width: 44},
			MaxSize:   Size{Width: 44},
			OnClicked: onClick,
		}
	}

	copyBtn := func(edit **walk.LineEdit) PushButton {
		return smallBtn("复制", func() {
			if *edit != nil {
				_ = walk.Clipboard().SetText((*edit).Text())
			}
		})
	}

	currentSelectedMode := func() string {
		if modeCombo == nil {
			return domain.DefaultWebOpenMode
		}
		idx := modeCombo.CurrentIndex()
		if idx >= 0 && idx < len(webOpenModeOptions) {
			return webOpenModeOptions[idx].Value
		}
		return domain.DefaultWebOpenMode
	}

	var activeDlg *walk.Dialog

	res := e.RunModalEditor("editor_controller", EditorConfig{
		Title:         "Web 面板设置",
		Width:         440,
		MinHeight:     265,
		AcceptBtnText: "确定",
		OnReady: func(dlg *walk.Dialog) {
			activeDlg = dlg
			isInitializing = true
			if modeCombo != nil {
				_ = modeCombo.SetCurrentIndex(findWebOpenModeIndex(initialMode))
			}
			isInitializing = false
		},
		Widgets: []Widget{
			GroupBox{
				Title:  "控制器连接",
				Layout: Grid{Columns: 2, Spacing: 6, Margins: Margins{Left: 8, Top: 10, Right: 8, Bottom: 8}},
				Children: []Widget{
					Label{
						Text:      "监听地址:",
						Alignment: AlignHNearVCenter,
					},
					Composite{
						Layout: HBox{MarginsZero: true, Spacing: 4},
						Children: []Widget{
							LineEdit{AssignTo: &addrEdit, Text: current.Addr},
							copyBtn(&addrEdit),
							smallBtn("默认", func() { addrEdit.SetText(domain.DefaultExternalController) }),
						},
					},
					Label{
						Text:      "访问密钥:",
						Alignment: AlignHNearVCenter,
					},
					Composite{
						Layout: HBox{MarginsZero: true, Spacing: 4},
						Children: []Widget{
							LineEdit{AssignTo: &secretEdit, Text: current.Secret},
							copyBtn(&secretEdit),
							smallBtn("生成", func() { secretEdit.SetText(random.String(domain.DefaultSecretLength)) }),
						},
					},
				},
			},
			GroupBox{
				Title:  "启动偏好",
				Layout: VBox{Margins: Margins{Left: 8, Top: 10, Right: 8, Bottom: 8}, Spacing: 6},
				Children: []Widget{
					Composite{
						Layout: HBox{MarginsZero: true, Spacing: 6},
						Children: []Widget{
							Label{
								Text:      "面板打开方式:",
								Alignment: AlignHNearVCenter,
							},
							ComboBox{
								AssignTo: &modeCombo,
								Model:    webOpenModeLabels(),
							},
							HSpacer{},
						},
					},
					Composite{
						Layout: Grid{Columns: 2, MarginsZero: true, Spacing: 6},
						Children: []Widget{
							CheckBox{
								AssignTo: &customBrowserCheck,
								Text:     "自定义浏览器:",
								Checked:  current.CustomBrowserEnabled,
								OnCheckedChanged: func() {
									if isInitializing {
										return
									}
									if customBrowserCheck.Checked() && strings.TrimSpace(pathEdit.Text()) == "" {
										pathEdit.SetFocus()
									}
								},
							},
							Composite{
								Layout: HBox{MarginsZero: true, Spacing: 4},
								Children: []Widget{
									LineEdit{
										AssignTo:  &pathEdit,
										Text:      current.CustomBrowserPath,
										CueBanner: "浏览器可执行文件 (.exe)",
										OnTextChanged: func() {
											if isInitializing {
												return
											}
											if strings.TrimSpace(pathEdit.Text()) != "" && !customBrowserCheck.Checked() {
												customBrowserCheck.SetChecked(true)
											}
										},
									},
									smallBtn("浏览", func() {
										resolvedCurrent := domain.ResolveBrowserPath(pathEdit.Text())
										selectedPath, ok := RunOpenBrowserExeDialog(activeDlg, resolvedCurrent)
										if !ok || selectedPath == "" {
											return
										}
										pathEdit.SetText(selectedPath)
										customBrowserCheck.SetChecked(true)
									}),
									smallBtn("清空", func() {
										pathEdit.SetText("")
										customBrowserCheck.SetChecked(false)
										pathEdit.SetFocus()
									}),
								},
							},
							CheckBox{
								AssignTo: &onlineCheck,
								Text:     "在线面板地址:",
								Checked:  current.RemoteWebUI,
								OnCheckedChanged: func() {
									if isInitializing {
										return
									}
									if onlineCheck.Checked() && strings.TrimSpace(remoteURLEdit.Text()) == "" {
										remoteURLEdit.SetFocus()
									}
								},
							},
							Composite{
								Layout: HBox{MarginsZero: true, Spacing: 4},
								Children: []Widget{
									LineEdit{
										AssignTo:  &remoteURLEdit,
										Text:      current.RemoteWebUIURL,
										CueBanner: "HTTP / HTTPS 面板地址",
										OnTextChanged: func() {
											if isInitializing {
												return
											}
											if strings.TrimSpace(remoteURLEdit.Text()) != "" && !onlineCheck.Checked() {
												onlineCheck.SetChecked(true)
											}
										},
									},
									copyBtn(&remoteURLEdit),
									smallBtn("默认", func() { remoteURLEdit.SetText(domain.DefaultRemoteWebUIURL) }),
								},
							},
						},
					},
				},
			},
		},
		OnAccept: func(dlg *walk.Dialog) (bool, error) {
			addr := strings.TrimSpace(addrEdit.Text())
			secret := strings.TrimSpace(secretEdit.Text())
			rURL := strings.TrimSpace(remoteURLEdit.Text())
			mode := currentSelectedMode()
			isCustom := customBrowserCheck.Checked()
			cPath := strings.TrimSpace(pathEdit.Text())

			showError := func(target *walk.LineEdit, msg string) (bool, error) {
				if target != nil {
					target.SetFocus()
				}
				RunErrorDialog(dlg, "保存失败", msg)
				return false, nil
			}

			switch {
			case !netutil.IsValidHostPort(addr):
				return showError(addrEdit, "监听地址格式无效")
			case netutil.IsPublicAddress(addr) && secret == "":
				return showError(secretEdit, "公网监听时密钥不能为空")
			case secret != "" && !domain.IsValidSecret(secret):
				return showError(secretEdit, "密钥包含非法字符")
			}

			if !onlineCheck.Checked() && rURL == "" {
				rURL = domain.DefaultRemoteWebUIURL
			}
			if !netutil.IsValidHTTPURL(rURL) {
				return showError(remoteURLEdit, "在线面板地址无效")
			}

			if isCustom {
				if cPath == "" {
					return showError(pathEdit, "请输入浏览器路径")
				}
				if !domain.IsValidBrowserExecutable(cPath) {
					return showError(pathEdit, "浏览器路径无效或文件不存在")
				}
				if mode == domain.WebOpenModeApp && !domain.IsChromiumBrowserExecutable(cPath) {
					ret := ShowNativeMsgBox(
						dlg,
						"提示",
						"app 模式仅支持 Chromium 浏览器，是否继续保存？",
						walk.MsgBoxOKCancel|walk.MsgBoxIconQuestion,
						func(win.HWND) {},
					)
					if ret != win.IDOK {
						pathEdit.SetFocus()
						return false, nil
					}
				}
			}

			result = domain.ControllerSettings{
				Addr:                 addr,
				Secret:               secret,
				RemoteWebUI:          onlineCheck.Checked(),
				RemoteWebUIURL:       rURL,
				WebOpenMode:          mode,
				CustomBrowserEnabled: isCustom,
				CustomBrowserPath:    cPath,
			}
			return true, nil
		},
	})

	return result, res.Accepted
}
