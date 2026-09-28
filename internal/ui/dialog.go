package ui

import (
	"strings"
	"unsafe"

	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"
	"github.com/tailscale/win"
)

func getValidOwner() walk.Form {
	if GlobalEngine != nil {
		if GlobalEngine.Dashboard != nil && GlobalEngine.Dashboard.window != nil {
			hwnd := GlobalEngine.Dashboard.window.Handle()
			if win.IsWindowVisible(hwnd) && !win.IsIconic(hwnd) {
				return GlobalEngine.Dashboard.window
			}
		}
		if GlobalEngine.mw != nil {
			return GlobalEngine.mw
		}
	}
	return nil
}

func OpenYAMLFileDialog() (string, bool) {
	if GlobalEngine == nil || GlobalEngine.app == nil {
		return "", false
	}
	type fileResult struct {
		Path string
		OK   bool
	}
	resultCh := make(chan fileResult)

	GlobalEngine.app.Synchronize(func() {
		dlg := new(walk.FileDialog)
		dlg.Title = "选择本地 YAML 配置文件"
		dlg.Filter = "YAML 配置文件 (*.yaml;*.yml)|*.yaml;*.yml|所有文件 (*.*)|*.*"
		ok, _ := dlg.ShowOpen(getValidOwner())
		resultCh <- fileResult{Path: dlg.FilePath, OK: ok}
	})
	res := <-resultCh
	return res.Path, res.OK
}

func autoWrapText(text string, maxVisualWidth int) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	var result []string
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		runes := []rune(line)
		if len(runes) == 0 {
			result = append(result, "")
			continue
		}
		var currentLine []rune
		currentWidth := 0
		for _, r := range runes {
			w := 1
			if r > 255 {
				w = 2
			}
			if currentWidth+w > maxVisualWidth {
				result = append(result, string(currentLine))
				currentLine = []rune{r}
				currentWidth = w
			} else {
				currentLine = append(currentLine, r)
				currentWidth += w
			}
		}
		if len(currentLine) > 0 {
			result = append(result, string(currentLine))
		}
	}
	return strings.Join(result, "\r\n")
}

func centerDialog(dlg *walk.Dialog, owner walk.Form, hActive win.HWND) {
	var rect win.RECT
	win.GetWindowRect(dlg.Handle(), &rect)
	dlgW := rect.Right - rect.Left
	dlgH := rect.Bottom - rect.Top

	var workArea win.RECT
	win.SystemParametersInfo(0x0030, 0, unsafe.Pointer(&workArea), 0)

	var x, y int32
	shouldFollowOwner := owner != nil && owner.Visible() && !win.IsIconic(owner.Handle())

	if shouldFollowOwner && hActive != 0 && hActive != owner.Handle() {
		shouldFollowOwner = false
	}

	if shouldFollowOwner {
		var pRect win.RECT
		win.GetWindowRect(owner.Handle(), &pRect)
		x = pRect.Left + (pRect.Right-pRect.Left-dlgW)/2
		y = pRect.Top + (pRect.Bottom-pRect.Top-dlgH)/2
	} else {
		x = workArea.Left + (workArea.Right-workArea.Left-dlgW)/2
		y = workArea.Top + (workArea.Bottom-workArea.Top-dlgH)/2
	}

	if x < workArea.Left {
		x = workArea.Left
	} else if x+dlgW > workArea.Right {
		x = workArea.Right - dlgW
	}
	
	if y < workArea.Top {
		y = workArea.Top
	} else if y+dlgH > workArea.Bottom {
		y = workArea.Bottom - dlgH
	}

	win.SetWindowPos(dlg.Handle(), win.HWND_TOP, x, y, 0, 0, win.SWP_NOSIZE)
}

func RunAlertDialog(owner walk.Form, title, message string, icon *walk.Icon, beep uint32) {
	parent := owner
	if parent == nil {
		parent = getValidOwner()
	}

	hActive := win.GetForegroundWindow()
	
	safeMsg := autoWrapText(message, 55)
	var dlg *walk.Dialog
	var acceptPB *walk.PushButton

	err := Dialog{
		AssignTo:      &dlg,
		Title:         title,
		MinSize:       Size{Width: 320, Height: 125},
		Layout:        VBox{Margins: Margins{Left: 15, Top: 20, Right: 15, Bottom: 12}, Spacing: 12},
		DefaultButton: &acceptPB,
		Children: []Widget{
			Composite{
				Layout: HBox{MarginsZero: true, Spacing: 15},
				Children: []Widget{
					Composite{
						Layout:    VBox{MarginsZero: true},
						Alignment: AlignHNearVNear,
						Children: []Widget{
							ImageView{Image: icon, MinSize: Size{Width: 32, Height: 32}},
							VSpacer{},
						},
					},
					TextLabel{Text: safeMsg},
				},
			},
			VSpacer{},
			Composite{
				Layout: HBox{MarginsZero: true},
				Children: []Widget{
					HSpacer{},
					PushButton{AssignTo: &acceptPB, Text: "确定", MinSize: Size{Width: 90, Height: 26}, OnClicked: func() { dlg.Accept() }},
				},
			},
		},
	}.Create(parent)

	if err != nil {
		return
	}

	dlg.Starting().Attach(func() { centerDialog(dlg, parent, hActive); win.MessageBeep(beep) })

	dlg.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {

		if parent != nil && parent.Visible() && !win.IsIconic(parent.Handle()) {
			win.SetForegroundWindow(parent.Handle())
			win.SetFocus(parent.Handle())
		} else if hActive != 0 && win.IsWindowVisible(hActive) && !win.IsIconic(hActive) {
			win.SetForegroundWindow(hActive)
			win.SetFocus(hActive)
		}
	})

	dlg.Run()
}

func RunConfirmDialog(owner walk.Form, title, message string) bool {
	parent := owner
	if parent == nil {
		parent = getValidOwner()
	}
	safeMsg := autoWrapText(message, 55)
	var dlg *walk.Dialog
	var acceptPB, cancelPB *walk.PushButton
	accepted := false

	err := Dialog{
		AssignTo:      &dlg,
		Title:         title,
		MinSize:       Size{Width: 320, Height: 125},
		Layout:        VBox{Margins: Margins{Left: 15, Top: 20, Right: 15, Bottom: 12}, Spacing: 12},
		DefaultButton: &acceptPB,
		CancelButton:  &cancelPB,
		Children: []Widget{
			Composite{
				Layout: HBox{MarginsZero: true, Spacing: 15},
				Children: []Widget{
					Composite{
						Layout:    VBox{MarginsZero: true},
						Alignment: AlignHNearVNear,
						Children: []Widget{
							ImageView{Image: walk.IconQuestion(), MinSize: Size{Width: 32, Height: 32}},
							VSpacer{},
						},
					},
					TextLabel{Text: safeMsg},
				},
			},
			VSpacer{},
			Composite{
				Layout: HBox{MarginsZero: true, Spacing: 10},
				Children: []Widget{
					HSpacer{},
					PushButton{AssignTo: &acceptPB, Text: "确定", MinSize: Size{Width: 90, Height: 26}, OnClicked: func() { accepted = true; dlg.Accept() }},
					PushButton{AssignTo: &cancelPB, Text: "取消", MinSize: Size{Width: 90, Height: 26}, OnClicked: func() { dlg.Cancel() }},
				},
			},
		},
	}.Create(parent)

	if err != nil {
		return false
	}

	dlg.Starting().Attach(func() { centerDialog(dlg, parent); win.MessageBeep(win.MB_ICONQUESTION) })

	dlg.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		if parent != nil && parent.Visible() {
			win.SetForegroundWindow(parent.Handle())
		}
	})

	dlg.Run()
	return accepted
}

func RunErrorDialog(owner walk.Form, title, message string) {
	RunAlertDialog(owner, title, message, walk.IconWarning(), win.MB_ICONWARNING)
}

func ShowErrorMessage(owner walk.Form, title, message string) {
	if GlobalEngine != nil && GlobalEngine.app != nil {
		GlobalEngine.app.Synchronize(func() {
			RunAlertDialog(owner, title, message, walk.IconWarning(), win.MB_ICONWARNING)
		})
	}
}

func ShowInfoMessage(owner walk.Form, title, message string) {
	if GlobalEngine != nil && GlobalEngine.app != nil {
		GlobalEngine.app.Synchronize(func() {
			RunAlertDialog(owner, title, message, walk.IconInformation(), win.MB_ICONINFORMATION)
		})
	}
}

func ShowConfirmMessage(owner walk.Form, title, message string) bool {
	if GlobalEngine == nil || GlobalEngine.app == nil {
		return false
	}
	resultCh := make(chan bool)
	GlobalEngine.app.Synchronize(func() {
		resultCh <- RunConfirmDialog(owner, title, message)
	})
	return <-resultCh
}

func ShowInfoModeless(owner walk.Form, title, message string) {
	ShowInfoMessage(owner, title, message)
}
