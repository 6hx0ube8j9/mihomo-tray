package ui

import (
	"strings"
	"unsafe"

	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"
	"github.com/tailscale/win"
)

func getValidOwner() walk.Form {
	if globalUIEngine != nil {
		if globalUIEngine.dashboardWindow != nil {
			hwnd := globalUIEngine.dashboardWindow.Handle()
			if win.IsWindowVisible(hwnd) && !win.IsIconic(hwnd) {
				return globalUIEngine.dashboardWindow
			}
		}
		if globalUIEngine.mw != nil {
			return globalUIEngine.mw
		}
	}
	return nil
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

func centerDialog(dlg *walk.Dialog, owner walk.Form) {
	var rect win.RECT
	win.GetWindowRect(dlg.Handle(), &rect)
	dlgW := rect.Right - rect.Left
	dlgH := rect.Bottom - rect.Top

	var x, y int32

	if owner != nil && owner.Visible() && !win.IsIconic(owner.Handle()) {
		var pRect win.RECT
		win.GetWindowRect(owner.Handle(), &pRect)
		pW := pRect.Right - pRect.Left
		pH := pRect.Bottom - pRect.Top
		x = pRect.Left + (pW-dlgW)/2
		y = pRect.Top + (pH-dlgH)/2
	} else {
		var workArea win.RECT
		if win.SystemParametersInfo(0x0030, 0, unsafe.Pointer(&workArea), 0) {
			screenW := workArea.Right - workArea.Left
			screenH := workArea.Bottom - workArea.Top
			x = workArea.Left + (screenW-dlgW)/2
			y = workArea.Top + (screenH-dlgH)/2
		}
	}

	win.SetWindowPos(dlg.Handle(), win.HWND_TOP, x, y, 0, 0, win.SWP_NOSIZE)
}

func OpenYAMLFileDialog() (string, bool) {
	if globalUIEngine == nil || globalUIEngine.app == nil {
		return "", false
	}

	type fileResult struct {
		Path string
		OK   bool
	}
	resultCh := make(chan fileResult)

	globalUIEngine.app.Synchronize(func() {
		dlg := new(walk.FileDialog)
		dlg.Title = "选择本地 YAML 配置文件"
		dlg.Filter = "YAML 配置文件 (*.yaml;*.yml)|*.yaml;*.yml|所有文件 (*.*)|*.*"

		ok, _ := dlg.ShowOpen(getValidOwner())
		resultCh <- fileResult{Path: dlg.FilePath, OK: ok}
	})

	res := <-resultCh
	return res.Path, res.OK
}

func RunErrorDialog(owner walk.Form, title, message string) {
	parent := owner
	if parent == nil {
		parent = getValidOwner()
	}

	safeMsg := autoWrapText(message, 64)
	hActive := win.GetForegroundWindow()

	var dlg *walk.Dialog
	var acceptPB *walk.PushButton

	err := Dialog{
		AssignTo:      &dlg,
		Title:         title,
		MinSize:       Size{Width: 320, Height: 150},
		Layout:        VBox{Margins: Margins{Top: 15, Bottom: 15, Left: 15, Right: 15}, Spacing: 15},
		DefaultButton: &acceptPB,
		Children: []Widget{
			Composite{
				Layout: HBox{MarginsZero: true, Spacing: 15},
				Children: []Widget{
					Composite{
						Layout: VBox{MarginsZero: true},
						Children: []Widget{
							ImageView{Image: walk.IconWarning(), MinSize: Size{Width: 40, Height: 40}},
							VSpacer{},
						},
					},
					Composite{
						Layout: VBox{MarginsZero: true}, // 回归自然放平
						Children: []Widget{
							TextLabel{Text: safeMsg},
							VSpacer{},
						},
					},
				},
			},
			VSpacer{},
			Composite{
				Layout: HBox{MarginsZero: true},
				Children: []Widget{
					HSpacer{},
					PushButton{
						AssignTo:  &acceptPB,
						Text:      "确定",
						MinSize:   Size{Width: 80, Height: 26},
						OnClicked: func() { dlg.Accept() },
					},
				},
			},
		},
	}.Create(parent)

	if err != nil {
		return
	}

	dlg.Starting().Attach(func() {
		centerDialog(dlg, parent)
		win.MessageBeep(win.MB_ICONWARNING)
	})

	dlg.Run()

	if parent != nil && parent.Visible() && !win.IsIconic(parent.Handle()) {
		win.SetForegroundWindow(parent.Handle())
		win.SetFocus(parent.Handle())
	} else if hActive != 0 && win.IsWindowVisible(hActive) && !win.IsIconic(hActive) {
		win.SetForegroundWindow(hActive)
		win.SetFocus(hActive)
	}
}

func ShowErrorMessage(owner walk.Form, title, message string) {
	if globalUIEngine == nil || globalUIEngine.app == nil {
		return
	}
	globalUIEngine.app.Synchronize(func() {
		RunErrorDialog(owner, title, message)
	})
}

func RunConfirmDialog(owner walk.Form, title, message string) bool {
	parent := owner
	if parent == nil {
		parent = getValidOwner()
	}

	safeMsg := autoWrapText(message, 64)
	hActive := win.GetForegroundWindow()

	var dlg *walk.Dialog
	var acceptPB *walk.PushButton
	var cancelPB *walk.PushButton
	accepted := false

	err := Dialog{
		AssignTo:      &dlg,
		Title:         title,
		MinSize:       Size{Width: 320, Height: 150},
		Layout:        VBox{Margins: Margins{Top: 15, Bottom: 15, Left: 15, Right: 15}, Spacing: 15},
		DefaultButton: &acceptPB,
		CancelButton:  &cancelPB,
		Children: []Widget{
			Composite{
				Layout: HBox{MarginsZero: true, Spacing: 15},
				Children: []Widget{
					Composite{
						Layout: VBox{MarginsZero: true},
						Children: []Widget{
							ImageView{Image: walk.IconQuestion(), MinSize: Size{Width: 40, Height: 40}},
							VSpacer{},
						},
					},
					Composite{
						Layout: VBox{MarginsZero: true},
						Children: []Widget{
							TextLabel{Text: safeMsg},
							VSpacer{},
						},
					},
				},
			},
			VSpacer{},
			Composite{
				Layout: HBox{MarginsZero: true},
				Children: []Widget{
					HSpacer{},
					PushButton{
						AssignTo:  &acceptPB,
						Text:      "确定",
						MinSize:   Size{Width: 80, Height: 26},
						OnClicked: func() { accepted = true; dlg.Accept() },
					},
					PushButton{
						AssignTo:  &cancelPB,
						Text:      "取消",
						MinSize:   Size{Width: 80, Height: 26},
						OnClicked: func() { dlg.Cancel() },
					},
				},
			},
		},
	}.Create(parent)

	if err != nil {
		return false
	}

	dlg.Starting().Attach(func() {
		centerDialog(dlg, parent)
		win.MessageBeep(win.MB_ICONQUESTION)
	})

	dlg.Run()

	if parent != nil && parent.Visible() && !win.IsIconic(parent.Handle()) {
		win.SetForegroundWindow(parent.Handle())
		win.SetFocus(parent.Handle())
	} else if hActive != 0 && win.IsWindowVisible(hActive) && !win.IsIconic(hActive) {
		win.SetForegroundWindow(hActive)
		win.SetFocus(hActive)
	}

	return accepted
}

func ShowConfirmMessage(owner walk.Form, title, message string) bool {
	if globalUIEngine == nil || globalUIEngine.app == nil {
		return false
	}

	resultCh := make(chan bool)
	globalUIEngine.app.Synchronize(func() {
		res := RunConfirmDialog(owner, title, message)
		resultCh <- res
	})
	return <-resultCh
}
