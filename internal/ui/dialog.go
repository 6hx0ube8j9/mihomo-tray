package ui

import (
	"unsafe"

	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"
	"github.com/tailscale/win"
)

func getOwner() walk.Form {
	if globalUIEngine != nil {
		if globalUIEngine.panelWindow != nil && globalUIEngine.panelWindow.Visible() {
			return globalUIEngine.panelWindow
		}
	}
	return nil
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

		ok, _ := dlg.ShowOpen(getOwner())
		resultCh <- fileResult{Path: dlg.FilePath, OK: ok}
	})

	res := <-resultCh
	return res.Path, res.OK
}

func centerDialog(dlg *walk.Dialog, hActive win.HWND, fallback walk.Form) {
	var rect win.RECT
	win.GetWindowRect(dlg.Handle(), &rect)
	dlgW := rect.Right - rect.Left
	dlgH := rect.Bottom - rect.Top

	var targetHWND win.HWND

	if hActive != 0 && win.IsWindowVisible(hActive) {
		targetHWND = hActive
	} else if fallback != nil && fallback.Visible() {
		targetHWND = fallback.Handle()
	}

	var x, y int32

	if targetHWND != 0 {
		var pRect win.RECT
		win.GetWindowRect(targetHWND, &pRect)
		pW := pRect.Right - pRect.Left
		pH := pRect.Bottom - pRect.Top
		x = pRect.Left + (pW-dlgW)/2
		y = pRect.Top + (pH-dlgH)/2
	} else {
		// 最终兜底：屏幕中央
		var workArea win.RECT
		if win.SystemParametersInfo(0x0030, 0, unsafe.Pointer(&workArea), 0) { // SPI_GETWORKAREA
			screenW := workArea.Right - workArea.Left
			screenH := workArea.Bottom - workArea.Top
			x = workArea.Left + (screenW-dlgW)/2
			y = workArea.Top + (screenH-dlgH)/2
		}
	}

	win.SetWindowPos(dlg.Handle(), win.HWND_TOP, x, y, 0, 0, win.SWP_NOSIZE)
}

func ShowErrorMessage(owner walk.Form, title, message string) {
	if globalUIEngine == nil || globalUIEngine.app == nil {
		return
	}

	globalUIEngine.app.Synchronize(func() {
		parent := owner
		if parent == nil {
			parent = getOwner()
		}

		hActive := win.GetForegroundWindow()

		var dlg *walk.Dialog
		var acceptPB *walk.PushButton

		err := Dialog{
			AssignTo:      &dlg,
			Title:         title,
			MinSize:       Size{Width: 350, Height: 150},
			Layout:        VBox{Margins: Margins{Top: 15, Bottom: 15, Left: 15, Right: 15}, Spacing: 15},
			DefaultButton: &acceptPB,
			Children: []Widget{
				Composite{
					Layout: HBox{MarginsZero: true, Spacing: 15},
					Children: []Widget{
						Composite{
							Layout: VBox{MarginsZero: true},
							Children: []Widget{
								ImageView{Image: walk.IconWarning(), MinSize: Size{Width: 32, Height: 32}},
								VSpacer{},
							},
						},
						Label{Text: message},
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
			centerDialog(dlg, hActive, parent)
			win.MessageBeep(win.MB_ICONWARNING)
		})

		dlg.Run()

		if hActive != 0 {
			win.SetForegroundWindow(hActive)
			win.SetFocus(hActive)
		}
	})
}

func ShowConfirmMessage(owner walk.Form, title, message string) bool {
	if globalUIEngine == nil || globalUIEngine.app == nil {
		return false
	}

	resultCh := make(chan bool)

	globalUIEngine.app.Synchronize(func() {
		parent := owner
		if parent == nil {
			parent = getOwner()
		}

		hActive := win.GetForegroundWindow()

		var dlg *walk.Dialog
		var acceptPB *walk.PushButton
		var cancelPB *walk.PushButton
		accepted := false

		err := Dialog{
			AssignTo:      &dlg,
			Title:         title,
			MinSize:       Size{Width: 350, Height: 150},
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
								ImageView{Image: walk.IconQuestion(), MinSize: Size{Width: 32, Height: 32}},
								VSpacer{},
							},
						},
						Label{Text: message},
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
			resultCh <- false
			return
		}

		dlg.Starting().Attach(func() {
			centerDialog(dlg, hActive, parent)
			win.MessageBeep(win.MB_ICONQUESTION)
		})

		dlg.Run()

		if hActive != 0 {
			win.SetForegroundWindow(hActive)
			win.SetFocus(hActive)
		}

		resultCh <- accepted
	})

	return <-resultCh
}
