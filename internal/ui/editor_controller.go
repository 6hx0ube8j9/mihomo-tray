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

const controllerLabelColWidth = 96

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

func smallActionBtn(text string, onClick func()) PushButton {
	return PushButton{
		Text:      text,
		MinSize:   Size{Width: 48},
		MaxSize:   Size{Width: 48},
		OnClicked: onClick,
	}
}

func copyLineEditBtn(edit **walk.LineEdit) PushButton {
	return smallActionBtn("复制", func() {
		if *edit != nil {
			_ = walk.Clipboard().SetText((*edit).Text())
		}
	})
}

type controllerEditorForm struct {
	current        domain.ControllerSettings
	result         domain.ControllerSettings
	activeDlg      *walk.Dialog
	isInitializing bool

	addrEdit           *walk.LineEdit
	secretEdit         *walk.LineEdit
	remoteURLEdit      *walk.LineEdit
	pathEdit           *walk.LineEdit
	onlineCheck        *walk.CheckBox
	customBrowserCheck *walk.CheckBox
	modeCombo          *walk.ComboBox
}

func (e *Engine) ShowControllerEditor(current domain.ControllerSettings) (domain.ControllerSettings, bool) {
	form := &controllerEditorForm{
		current:        current,
		result:         current,
		isInitializing: true,
	}

	res := e.RunModalEditor("editor_controller", EditorConfig{
		Title:         "Web 面板设置",
		Width:         440,
		AcceptBtnText: "确定",
		OnReady:       form.onReady,
		Widgets:       form.widgets(),
		OnAccept:      form.onAccept,
	})

	return form.result, res.Accepted
}

func (f *controllerEditorForm) onReady(dlg *walk.Dialog) {
	f.activeDlg = dlg
	f.isInitializing = false
}

func (f *controllerEditorForm) widgets() []Widget {

	return []Widget{
		GroupBox{
			Title:  "控制器连接",
			Layout: Grid{Columns: 2},
			Children: []Widget{
				Label{
					Text:      "监听地址",
					Alignment: AlignHNearVCenter,
					MinSize:   Size{Width: controllerLabelColWidth},
				},
				Composite{
					Layout: HBox{MarginsZero: true},
					Children: []Widget{
						LineEdit{
							AssignTo: &f.addrEdit,
							Text:     f.current.Addr,
						},
						copyLineEditBtn(&f.addrEdit),
						smallActionBtn("默认", func() { f.addrEdit.SetText(domain.DefaultExternalController) }),
					},
				},
				Label{
					Text:      "访问密钥",
					Alignment: AlignHNearVCenter,
					MinSize:   Size{Width: controllerLabelColWidth},
				},
				Composite{
					Layout: HBox{MarginsZero: true},
					Children: []Widget{
						LineEdit{AssignTo: &f.secretEdit, Text: f.current.Secret},
						copyLineEditBtn(&f.secretEdit),
						smallActionBtn("生成", func() { f.secretEdit.SetText(random.String(domain.DefaultSecretLength)) }),
					},
				},
			},
		},
		GroupBox{
			Title:  "启动偏好",
			Layout: Grid{Columns: 2},
			Children: []Widget{
				Label{
					Text:      "Web 打开方式",
					Alignment: AlignHNearVCenter,
					MinSize:   Size{Width: controllerLabelColWidth},
				},
				Composite{
					Layout: HBox{MarginsZero: true},
					Children: []Widget{
						ComboBox{
							AssignTo:     &f.modeCombo,
							Model:        webOpenModeLabels(),
							CurrentIndex: findWebOpenModeIndex(f.current.WebOpenMode),
						},
						HSpacer{},
					},
				},
				CheckBox{
					AssignTo:         &f.customBrowserCheck,
					Text:             "自定义浏览器",
					Checked:          f.current.CustomBrowserEnabled,
					MinSize:          Size{Width: controllerLabelColWidth},
					OnCheckedChanged: func() { f.focusIfCheckedEmpty(f.customBrowserCheck, f.pathEdit) },
				},
				Composite{
					Layout: HBox{MarginsZero: true},
					Children: []Widget{
						LineEdit{
							AssignTo:  &f.pathEdit,
							Text:      f.current.CustomBrowserPath,
							CueBanner: "浏览器可执行文件 (.exe)",
						},
						smallActionBtn("浏览", f.browseBrowserExe),
						smallActionBtn("清空", f.clearBrowserExe),
					},
				},
				CheckBox{
					AssignTo:         &f.onlineCheck,
					Text:             "在线面板地址",
					Checked:          f.current.RemoteWebUI,
					MinSize:          Size{Width: controllerLabelColWidth},
					OnCheckedChanged: func() { f.focusIfCheckedEmpty(f.onlineCheck, f.remoteURLEdit) },
				},
				Composite{
					Layout: HBox{MarginsZero: true},
					Children: []Widget{
						LineEdit{
							AssignTo: &f.remoteURLEdit,
							Text:     f.current.RemoteWebUIURL,
						},
						copyLineEditBtn(&f.remoteURLEdit),
						smallActionBtn("默认", func() { f.remoteURLEdit.SetText(domain.DefaultRemoteWebUIURL) }),
					},
				},
			},
		},
	}
}

func (f *controllerEditorForm) focusIfCheckedEmpty(check *walk.CheckBox, edit *walk.LineEdit) {
	if !f.isInitializing && check.Checked() && strings.TrimSpace(edit.Text()) == "" {
		edit.SetFocus()
	}
}

func (f *controllerEditorForm) browseBrowserExe() {
	selectedPath, ok := RunOpenBrowserExeDialog(f.activeDlg, f.pathEdit.Text())
	if !ok || selectedPath == "" {
		return
	}
	f.pathEdit.SetText(selectedPath)
	f.customBrowserCheck.SetChecked(true)
}

func (f *controllerEditorForm) clearBrowserExe() {
	f.pathEdit.SetText("")
	f.customBrowserCheck.SetChecked(false)
	f.pathEdit.SetFocus()
}

func (f *controllerEditorForm) selectedMode() string {
	if f.modeCombo == nil {
		return domain.DefaultWebOpenMode
	}
	if idx := f.modeCombo.CurrentIndex(); idx >= 0 && idx < len(webOpenModeOptions) {
		return webOpenModeOptions[idx].Value
	}
	return domain.DefaultWebOpenMode
}

func (f *controllerEditorForm) onAccept(dlg *walk.Dialog) (bool, error) {
	addr := strings.TrimSpace(f.addrEdit.Text())
	secret := strings.TrimSpace(f.secretEdit.Text())
	rURL := strings.TrimSpace(f.remoteURLEdit.Text())
	mode := f.selectedMode()
	isCustom := f.customBrowserCheck.Checked()
	cPath := strings.TrimSpace(f.pathEdit.Text())

	if !f.onlineCheck.Checked() && rURL == "" {
		rURL = domain.DefaultRemoteWebUIURL
	}

	fail := func(target *walk.LineEdit, msg string) (bool, error) {
		if target != nil {
			target.SetFocus()
		}
		RunErrorDialog(dlg, "保存失败", msg)
		return false, nil
	}

	switch {
	case !netutil.IsValidHostPort(addr):
		return fail(f.addrEdit, "监听地址格式无效")
	case netutil.IsPublicAddress(addr) && secret == "":
		return fail(f.secretEdit, "公网监听时密钥不能为空")
	case secret != "" && !domain.IsValidSecret(secret):
		return fail(f.secretEdit, "密钥包含非法字符")
	case !netutil.IsValidHTTPURL(rURL):
		return fail(f.remoteURLEdit, "在线面板地址无效")
	}

	if isCustom {
		if cPath == "" {
			return fail(f.pathEdit, "请输入浏览器路径")
		}
		if !domain.IsValidBrowserExecutable(cPath) {
			return fail(f.pathEdit, "浏览器路径无效或文件不存在")
		}
		if mode == domain.WebOpenModeApp && !domain.IsChromiumBrowserExecutable(cPath) {
			ret := ShowNativeMsgBox(
				dlg,
				"提示",
				"应用模式仅支持 Chromium 浏览器，是否继续保存？",
				walk.MsgBoxOKCancel|walk.MsgBoxIconQuestion,
				func(win.HWND) {},
			)
			if ret != win.IDOK {
				f.pathEdit.SetFocus()
				return false, nil
			}
		}
	}

	f.result = domain.ControllerSettings{
		Addr:                 addr,
		Secret:               secret,
		RemoteWebUI:          f.onlineCheck.Checked(),
		RemoteWebUIURL:       rURL,
		WebOpenMode:          mode,
		CustomBrowserEnabled: isCustom,
		CustomBrowserPath:    cPath,
	}
	return true, nil
}