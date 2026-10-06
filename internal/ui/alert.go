package ui

import (
	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"
	"github.com/tailscale/win"
)

func runBaseDialog(owner walk.Form, title, message string, icon *walk.Icon, beep uint32, isConfirm bool, onReady func(dlg *walk.Dialog)) bool {
	parent := owner
	hActive := win.GetForegroundWindow()
	safeMsg := autoWrapText(message, 55)
	
	var dlg *walk.Dialog
	var acceptPB, cancelPB *walk.PushButton
	accepted := false

	buttons := []Widget{
		HSpacer{},
		PushButton{AssignTo: &acceptPB, Text: "确定", MinSize: Size{Width: 90, Height: 26}, OnClicked: func() { accepted = true; dlg.Accept() }},
	}
	if isConfirm {
		buttons = append(buttons, PushButton{AssignTo: &cancelPB, Text: "取消", MinSize: Size{Width: 90, Height: 26}, OnClicked: func() { dlg.Cancel() }})
	}

	dlgConfig := Dialog{
		AssignTo:      &dlg,
		Title:         title,
		MinSize:       Size{Width: 320, Height: 125},
		MaxSize:       Size{Width: 320, Height: 125},
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
				Layout: HBox{MarginsZero: true, Spacing: 10},
				Children: buttons,
			},
		},
	}

	if isConfirm {
		dlgConfig.CancelButton = &cancelPB
	}

	if err := dlgConfig.Create(parent); err != nil {
		return false
	}
	
	if onReady != nil {
		onReady(dlg)
	}
	
	defer dlg.Dispose()
	
	dlg.Starting().Attach(func() { 
		lockWindowSize(dlg.Handle())
		centerDialog(dlg, parent, hActive)
		win.MessageBeep(beep) 
	})

	dlg.SizeChanged().Attach(func() {
		centerDialog(dlg, parent, hActive)
	})

	dlg.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		restoreFocus(parent, hActive)
	})

	dlg.Run()
	return accepted
}

func RunAlertDialog(owner walk.Form, title, message string, icon *walk.Icon, beep uint32) {
	runBaseDialog(owner, title, message, icon, beep, false, nil)
}

func RunConfirmDialog(owner walk.Form, title, message string) bool {
	return runBaseDialog(owner, title, message, walk.IconQuestion(), win.MB_ICONWARNING, true, nil)
}

func RunErrorDialog(owner walk.Form, title, message string) {
	RunAlertDialog(owner, title, message, walk.IconError(), win.MB_ICONERROR)
}
