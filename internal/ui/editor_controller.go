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
	{Value: domain.WebOpenModeAuto, Label: "自动 (Auto)"},
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
	var browseBtn, clearBtn, remoteCopyBtn, remoteDefaultBtn *walk.PushButton

	isInitializing := true

	const (
		leftColWidth   = 104
		smallBtnWidth  = 44
		actionColWidth = 92 // 44 + 4 + 44 = 92
		comboColWidth  = 138
	)

	smallBtn := func(assignTo **walk.PushButton, text string, enabled bool, onClick func()) PushButton {
		return PushButton{
			AssignTo:  assignTo,
			Text:      text,
			Enabled:   enabled,
			MinSize:   Size{Width: smallBtnWidth},
			MaxSize:   Size{Width: smallBtnWidth},
			OnClicked: onClick,
		}
	}

	copyBtn := func(assignTo **walk.PushButton, enabled bool, edit **walk.LineEdit) PushButton {
		return smallBtn(assignTo, "复制", enabled, func() {
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

	syncState := func() {
		if customBrowserCheck != nil && pathEdit != nil && browseBtn != nil && clearBtn != nil {
			isCustom := customBrowserCheck.Checked()
			pathEdit.SetEnabled(isCustom)
			browseBtn.SetEnabled(isCustom)
			clearBtn.SetEnabled(isCustom)
		}
		if onlineCheck != nil && remoteURLEdit != nil && remoteCopyBtn != nil && remoteDefaultBtn != nil {
			isOnline := onlineCheck.Checked()
			remoteURLEdit.SetEnabled(isOnline)
			remoteCopyBtn.SetEnabled(isOnline)
			remoteDefaultBtn.SetEnabled(isOnline)
		}
	}

	var activeDlg *walk.Dialog

	res := e.RunModalEditor("editor_controller", EditorConfig{
		Title:         "Web 面板设置",
		Width:         440,
		MinHeight:     245,
		AcceptBtnText: "确定",
		OnReady: func(dlg *walk.Dialog) {
			activeDlg = dlg
			isInitializing = true
			if modeCombo != nil {
				_ = modeCombo.SetCurrentIndex(findWebOpenModeIndex(initialMode))
			}
			isInitializing = false
			syncState()
		},
		Widgets: []Widget{
			GroupBox{
				Title:  "控制器连接",
				Layout: VBox{Margins: Margins{Left: 10, Top: 6, Right: 10, Bottom: 8}, Spacing: 6},
				Children: []Widget{
					Composite{
						Layout: HBox{MarginsZero: true, Spacing: 6},
						Children: []Widget{
							Label{
								Text:      "监听地址:",
								Alignment: AlignHNearVCenter,
								MinSize:   Size{Width: leftColWidth},
							},
							LineEdit{AssignTo: &addrEdit, Text: current.Addr},
							Composite{
								Layout:  HBox{MarginsZero: true, Spacing: 4},
								MinSize: Size{Width: actionColWidth},
								MaxSize: Size{Width: actionColWidth},
								Children: []Widget{
									copyBtn(nil, true, &addrEdit),
									smallBtn(nil, "默认", true, func() { addrEdit.SetText(domain.DefaultExternalController) }),
								},
							},
						},
					},
					Composite{
						Layout: HBox{MarginsZero: true, Spacing: 6},
						Children: []Widget{
							Label{
								Text:      "访问密钥:",
								Alignment: AlignHNearVCenter,
								MinSize:   Size{Width: leftColWidth},
							},
							LineEdit{AssignTo: &secretEdit, Text: current.Secret},
							Composite{
								Layout:  HBox{MarginsZero: true, Spacing: 4},
								MinSize: Size{Width: actionColWidth},
								MaxSize: Size{Width: actionColWidth},
								Children: []Widget{
									copyBtn(nil, true, &secretEdit),
									smallBtn(nil, "生成", true, func() { secretEdit.SetText(random.String(domain.DefaultSecretLength)) }),
								},
							},
						},
					},
				},
			},
			GroupBox{
				Title:  "启动偏好",
				Layout: VBox{Margins: Margins{Left: 10, Top: 6, Right: 10, Bottom: 8}, Spacing: 6},
				Children: []Widget{
					Composite{
						Layout: HBox{MarginsZero: true, Spacing: 6},
						Children: []Widget{
							Label{
								Text:      "Web 打开方式:",
								Alignment: AlignHNearVCenter,
								MinSize:   Size{Width: leftColWidth},
							},
							Composite{
								Layout:  HBox{MarginsZero: true},
								MinSize: Size{Width: comboColWidth},
								MaxSize: Size{Width: comboColWidth},
								Children: []Widget{
									ComboBox{
										AssignTo: &modeCombo,
										Model:    webOpenModeLabels(),
									},
								},
							},
							HSpacer{},
						},
					},
					Composite{
						Layout: HBox{MarginsZero: true, Spacing: 6},
						Children: []Widget{
							CheckBox{
								AssignTo: &customBrowserCheck,
								Text:     "自定义浏览器:",
								Checked:  current.CustomBrowserEnabled,
								MinSize:  Size{Width: leftColWidth},
								OnCheckedChanged: func() {
									if isInitializing {
										return
									}
									syncState()
									if customBrowserCheck.Checked() && strings.TrimSpace(pathEdit.Text()) == "" {
										pathEdit.SetFocus()
									}
								},
							},
							LineEdit{
								AssignTo: &pathEdit,
								Text:     current.CustomBrowserPath,
								Enabled:  current.CustomBrowserEnabled,
							},
							Composite{
								Layout:  HBox{MarginsZero: true, Spacing: 4},
								MinSize: Size{Width: actionColWidth},
								MaxSize: Size{Width: actionColWidth},
								Children: []Widget{
									smallBtn(&browseBtn, "浏览", current.CustomBrowserEnabled, func() {
										resolvedCurrent := domain.ResolveBrowserPath(pathEdit.Text())
										selectedPath, ok := RunOpenBrowserExeDialog(activeDlg, resolvedCurrent)
										if !ok || selectedPath == "" {
											return
										}
										pathEdit.SetText(selectedPath)
									}),
									smallBtn(&clearBtn, "清空", current.CustomBrowserEnabled, func() {
										pathEdit.SetText("")
										pathEdit.SetFocus()
									}),
								},
							},
						},
					},
					Composite{
						Layout: HBox{MarginsZero: true, Spacing: 6},
						Children: []Widget{
							CheckBox{
								AssignTo: &onlineCheck,
								Text:     "在线面板地址:",
								Checked:  current.RemoteWebUI,
								MinSize:  Size{Width: leftColWidth},
								OnCheckedChanged: func() {
									if isInitializing {
										return
									}
									syncState()
									if onlineCheck.Checked() && strings.TrimSpace(remoteURLEdit.Text()) == "" {
										remoteURLEdit.SetFocus()
									}
								},
							},
							LineEdit{
								AssignTo: &remoteURLEdit,
								Text:     current.RemoteWebUIURL,
								Enabled:  current.RemoteWebUI,
							},
							Composite{
								Layout:  HBox{MarginsZero: true, Spacing: 4},
								MinSize: Size{Width: actionColWidth},
								MaxSize: Size{Width: actionColWidth},
								Children: []Widget{
									copyBtn(&remoteCopyBtn, current.RemoteWebUI, &remoteURLEdit),
									smallBtn(&remoteDefaultBtn, "默认", current.RemoteWebUI, func() { remoteURLEdit.SetText(domain.DefaultRemoteWebUIURL) }),
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

			showError := func(msg string) (bool, error) {
				RunErrorDialog(dlg, "保存失败", msg)
				return false, nil
			}

			switch {
			case !netutil.IsValidHostPort(addr):
				return showError("监听地址格式无效")
			case netutil.IsPublicAddress(addr) && secret == "":
				return showError("公网监听时密钥不能为空")
			case secret != "" && !domain.IsValidSecret(secret):
				return showError("密钥包含非法字符")
			case !netutil.IsValidHTTPURL(rURL):
				return showError("在线面板地址无效")
			}

			if isCustom {
				if cPath == "" {
					return showError("请输入浏览器路径")
				}
				if !domain.IsValidBrowserExecutable(cPath) {
					return showError("浏览器路径无效或文件不存在")
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