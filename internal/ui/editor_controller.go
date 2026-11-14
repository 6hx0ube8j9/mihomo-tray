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

var webOpenModeOptions = []string{
	domain.WebOpenModeAuto,
	domain.WebOpenModeApp,
	domain.WebOpenModeBrowser,
	domain.WebOpenModeWebView,
}

func getWebOpenModeDescription(mode string) string {
	switch mode {
	case domain.WebOpenModeAuto:
		return "优先内置窗口，失败后自动降级"
	case domain.WebOpenModeApp:
		return "以无边框独立应用窗口运行"
	case domain.WebOpenModeBrowser:
		return "在普通浏览器标签页中打开"
	case domain.WebOpenModeWebView:
		return "仅使用内置 WebView2 窗口打开"
	default:
		return ""
	}
}

func findWebOpenModeIndex(mode string) int {
	normalized := domain.NormalizeWebOpenMode(mode)
	for i, m := range webOpenModeOptions {
		if m == normalized {
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
	var modeDescLabel *walk.Label
	var browseBtn *walk.PushButton

	isInitializing := true

	const (
		leftColWidth   = 100
		smallBtnWidth  = 44
		actionColWidth = 92 // 44 + 4 + 44 = 92
	)

	smallBtn := func(text string, onClick func()) PushButton {
		return PushButton{
			Text:      text,
			MinSize:   Size{Width: smallBtnWidth},
			MaxSize:   Size{Width: smallBtnWidth},
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
			return webOpenModeOptions[idx]
		}
		return domain.DefaultWebOpenMode
	}

	syncState := func() {
		if modeCombo != nil && modeDescLabel != nil {
			modeDescLabel.SetText(getWebOpenModeDescription(currentSelectedMode()))
		}
		if customBrowserCheck == nil || pathEdit == nil || browseBtn == nil {
			return
		}
		isChecked := customBrowserCheck.Checked()
		pathEdit.SetEnabled(isChecked)
		browseBtn.SetEnabled(isChecked)
	}

	var activeDlg *walk.Dialog

	res := e.RunModalEditor("editor_controller", EditorConfig{
		Title:         "Web 面板设置",
		Width:         520,
		MinHeight:     230,
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
				Layout: VBox{Margins: Margins{Left: 10, Top: 6, Right: 10, Bottom: 8}, Spacing: 5},
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
									copyBtn(&addrEdit),
									smallBtn("默认", func() { addrEdit.SetText(domain.DefaultExternalController) }),
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
									copyBtn(&secretEdit),
									smallBtn("生成", func() { secretEdit.SetText(random.String(domain.DefaultSecretLength)) }),
								},
							},
						},
					},
				},
			},
			GroupBox{
				Title:  "启动偏好",
				Layout: VBox{Margins: Margins{Left: 10, Top: 6, Right: 10, Bottom: 8}, Spacing: 5},
				Children: []Widget{
					Composite{
						Layout: HBox{MarginsZero: true, Spacing: 6},
						Children: []Widget{
							Label{
								Text:      "面板打开方式:",
								Alignment: AlignHNearVCenter,
								MinSize:   Size{Width: leftColWidth},
							},
							ComboBox{
								AssignTo:              &modeCombo,
								Model:                 webOpenModeOptions,
								MinSize:               Size{Width: 96},
								MaxSize:               Size{Width: 96},
								OnCurrentIndexChanged: func() {
									if !isInitializing {
										syncState()
									}
								},
							},
							Label{
								AssignTo:  &modeDescLabel,
								Text:      getWebOpenModeDescription(initialMode),
								Alignment: AlignHNearVCenter,
							},
						},
					},
					Composite{
						Layout: HBox{MarginsZero: true, Spacing: 6},
						Children: []Widget{
							CheckBox{
								AssignTo: &customBrowserCheck,
								Text:     "指定浏览器:",
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
							PushButton{
								AssignTo: &browseBtn,
								Text:     "浏览",
								MinSize:  Size{Width: smallBtnWidth},
								MaxSize:  Size{Width: smallBtnWidth},
								OnClicked: func() {
									resolvedCurrent := domain.ResolveBrowserPath(pathEdit.Text())
									selectedPath, ok := RunOpenBrowserExeDialog(activeDlg, resolvedCurrent)
									if !ok || selectedPath == "" {
										return
									}
									pathEdit.SetText(selectedPath)
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
							},
							LineEdit{AssignTo: &remoteURLEdit, Text: current.RemoteWebUIURL},
							Composite{
								Layout:  HBox{MarginsZero: true, Spacing: 4},
								MinSize: Size{Width: actionColWidth},
								MaxSize: Size{Width: actionColWidth},
								Children: []Widget{
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

			showError := func(msg string) (bool, error) {
				RunErrorDialog(dlg, "保存失败", msg)
				return false, nil
			}

			switch {
			case !netutil.IsValidHostPort(addr):
				return showError("监听地址格式错误。")
			case netutil.IsPublicAddress(addr) && secret == "":
				return showError("当前地址支持外网访问，密钥不能为空。")
			case secret != "" && !domain.IsValidSecret(secret):
				return showError("密钥包含非法字符，仅支持英文字母、数字及常用符号。")
			case !netutil.IsValidHTTPURL(rURL):
				return showError("在线面板地址格式错误，请输入有效的 HTTP/HTTPS 链接。")
			}

			if isCustom {
				if cPath == "" {
					return showError("请选择或输入指定浏览器路径。")
				}
				if !domain.IsValidBrowserExecutable(cPath) {
					return showError("指定浏览器文件不存在或不是有效的 .exe 程序。")
				}
				if mode == domain.WebOpenModeApp && !domain.IsChromiumBrowserExecutable(cPath) {
					ret := ShowNativeMsgBox(
						dlg,
						"提示",
						"app 模式仅支持 Chromium 类型浏览器，是否继续保存？",
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