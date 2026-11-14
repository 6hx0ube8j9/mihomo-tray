package ui

import (
	"strings"

	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"

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

	var addrEdit, secretEdit, remoteURLEdit, pathEdit, dirEdit *walk.LineEdit
	var onlineCheck, customPathCheck *walk.CheckBox
	var modeCombo *walk.ComboBox
	var modeDescLabel, dirLabel *walk.Label
	var browseBtn, genDirBtn *walk.PushButton

	isInitializing := true

	const (
		leftColWidth   = 108
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

	wideBtn := func(assignTo **walk.PushButton, text string, onClick func()) PushButton {
		return PushButton{
			AssignTo:  assignTo,
			Text:      text,
			MinSize:   Size{Width: actionColWidth},
			MaxSize:   Size{Width: actionColWidth},
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
		if modeCombo == nil || customPathCheck == nil || pathEdit == nil || dirEdit == nil || browseBtn == nil || genDirBtn == nil {
			return
		}

		mode := currentSelectedMode()
		if modeDescLabel != nil {
			modeDescLabel.SetText(getWebOpenModeDescription(mode))
		}

		isCustomAllowed := (mode == domain.WebOpenModeApp || mode == domain.WebOpenModeBrowser)
		isAppMode := (mode == domain.WebOpenModeApp)
		isChecked := customPathCheck.Checked()
		hasPath := strings.TrimSpace(pathEdit.Text()) != ""

		customPathCheck.SetEnabled(isCustomAllowed)
		pathEdit.SetEnabled(isCustomAllowed && isChecked)
		browseBtn.SetEnabled(isCustomAllowed && isChecked)

		if dirLabel != nil {
			dirLabel.SetEnabled(isAppMode && isChecked)
		}
		dirEdit.SetEnabled(isAppMode && isChecked)
		genDirBtn.SetEnabled(isAppMode && isChecked && hasPath)
	}

	var activeDlg *walk.Dialog

	res := e.RunModalEditor("editor_controller", EditorConfig{
		Title:         "Web 面板设置",
		Width:         460,
		MinHeight:     315,
		AcceptBtnText: "确定",
		OnReady: func(dlg *walk.Dialog) {
			activeDlg = dlg
			isInitializing = true
			if modeCombo != nil {
				_ = modeCombo.SetCurrentIndex(findWebOpenModeIndex(initialMode))
			}
			if customPathCheck != nil {
				customPathCheck.SetChecked(strings.TrimSpace(current.CustomBrowserPath) != "")
			}
			isInitializing = false
			syncState()
		},
		Widgets: []Widget{
			GroupBox{
				Title:  "控制器连接",
				Layout: VBox{Margins: Margins{Left: 10, Top: 10, Right: 10, Bottom: 10}, Spacing: 8},
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
				Layout: VBox{Margins: Margins{Left: 10, Top: 10, Right: 10, Bottom: 10}, Spacing: 8},
				Children: []Widget{
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
								AssignTo: &customPathCheck,
								Text:     "自定义路径:",
								MinSize:  Size{Width: leftColWidth},
								OnCheckedChanged: func() {
									if isInitializing {
										return
									}
									if !customPathCheck.Checked() {
										pathEdit.SetText("")
										dirEdit.SetText("")
									}
									syncState()
									if customPathCheck.Checked() && strings.TrimSpace(pathEdit.Text()) == "" {
										pathEdit.SetFocus()
									}
								},
							},
							LineEdit{
								AssignTo: &pathEdit,
								Text:     current.CustomBrowserPath,
								OnTextChanged: func() {
									if !isInitializing {
										syncState()
									}
								},
							},
							wideBtn(&browseBtn, "浏览", func() {
								selectedPath, ok := RunOpenBrowserExeDialog(activeDlg, pathEdit.Text())
								if !ok || selectedPath == "" {
									return
								}
								pathEdit.SetText(selectedPath)
								if currentSelectedMode() == domain.WebOpenModeApp && strings.TrimSpace(dirEdit.Text()) == "" {
									dirEdit.SetText(domain.GenerateRandomUserDataDir(selectedPath))
								}
								syncState()
							}),
						},
					},

					Composite{
						Layout: HBox{MarginsZero: true, Spacing: 6},
						Children: []Widget{
							Label{
								AssignTo:  &dirLabel,
								Text:      "目录名称:",
								Alignment: AlignHNearVCenter,
								MinSize:   Size{Width: leftColWidth},
							},
							LineEdit{
								AssignTo: &dirEdit,
								Text:     current.CustomUserDataDir,
							},
							wideBtn(&genDirBtn, "生成", func() {
								p := strings.TrimSpace(pathEdit.Text())
								if p == "" {
									return
								}
								dirEdit.SetText(domain.GenerateRandomUserDataDir(p))
							}),
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
			isCustom := customPathCheck.Checked()
			cPath := strings.TrimSpace(pathEdit.Text())
			cDir := strings.TrimSpace(dirEdit.Text())

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

			if mode == domain.WebOpenModeApp || mode == domain.WebOpenModeBrowser {
				if isCustom {
					if cPath == "" {
						return showError("请选择或输入自定义浏览器路径。")
					}
					if !domain.IsValidBrowserExecutable(cPath) {
						return showError("自定义浏览器文件不存在或路径无效。")
					}
					if mode == domain.WebOpenModeApp {
						if cDir == "" {
							return showError("独立数据目录名称不能为空。")
						}
						if !domain.IsValidUserDataDirName(cDir) {
							return showError("目录名称包含非法字符或相对路径符号。")
						}
					}
				} else {
					cPath = ""
					cDir = ""
				}
			}

			result = domain.ControllerSettings{
				Addr:              addr,
				Secret:            secret,
				RemoteWebUI:       onlineCheck.Checked(),
				RemoteWebUIURL:    rURL,
				WebOpenMode:       mode,
				CustomBrowserPath: cPath,
				CustomUserDataDir: cDir,
			}
			return true, nil
		},
	})

	return result, res.Accepted
}