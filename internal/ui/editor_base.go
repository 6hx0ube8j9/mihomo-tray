package ui

import (
	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"
	"github.com/tailscale/win"
)

type EditorConfig struct {
	Title         string
	Width         int
	MinHeight     int
	Widgets       []Widget
	OnAccept      func() (bool, error)
	AcceptBtnText string
	CancelBtnText string
	AssignTo      **walk.Dialog
}

type EditorResult struct {
	Accepted bool
	Error    error
}

func RunEditor(owner walk.Form, cfg EditorConfig) EditorResult {
	if cfg.AssignTo != nil && *cfg.AssignTo != nil {
		dlg := *cfg.AssignTo
		if dlg.Visible() {
			hwnd := dlg.Handle()
			if win.IsIconic(hwnd) {
				win.ShowWindow(hwnd, win.SW_RESTORE)
			}
			win.SetForegroundWindow(hwnd)
			dlg.SetFocus()
		}
		return EditorResult{Accepted: false}
	}

	if cfg.AcceptBtnText == "" {
		cfg.AcceptBtnText = "保存"
	}
	if cfg.CancelBtnText == "" {
		cfg.CancelBtnText = "取消"
	}

	hActive := win.GetForegroundWindow()

	var dlg *walk.Dialog
	var acceptPB, cancelPB *walk.PushButton
	var isAccepted bool
	var processErr error

	layoutChildren := append(cfg.Widgets,
		VSpacer{},
		Composite{
			Layout: HBox{MarginsZero: true, Spacing: 10},
			Children: []Widget{
				HSpacer{},
				PushButton{
					AssignTo: &acceptPB,
					Text:     cfg.AcceptBtnText,
					MinSize:  Size{Width: 80, Height: 26},
					OnClicked: func() {
						if cfg.OnAccept != nil {
							ok, err := cfg.OnAccept()
							if !ok {
								return
							}
							processErr = err
						}
						isAccepted = true
						dlg.Accept()
					},
				},
				PushButton{
					AssignTo: &cancelPB,
					Text:     cfg.CancelBtnText,
					MinSize:  Size{Width: 80, Height: 26},
					OnClicked: func() {
						dlg.Cancel()
					},
				},
			},
		},
	)

	err := Dialog{
		AssignTo:      cfg.AssignTo,
		Title:         cfg.Title,
		MinSize:       Size{Width: cfg.Width, Height: cfg.MinHeight},
		MaxSize:       Size{Width: cfg.Width, Height: cfg.MinHeight},
		Layout:        VBox{Margins: Margins{Left: 18, Top: 15, Right: 18, Bottom: 15}, Spacing: 12},
		DefaultButton: &acceptPB,
		CancelButton:  &cancelPB,
		Children:      layoutChildren,
	}.Create(owner)

	if err != nil {
		return EditorResult{Accepted: false, Error: err}
	}

	if cfg.AssignTo != nil {
		dlg = *cfg.AssignTo
	}

	defer dlg.Dispose()

	dlg.Starting().Attach(func() {
		hwnd := dlg.Handle()
		style := win.GetWindowLong(hwnd, win.GWL_STYLE)
		style &^= win.WS_THICKFRAME | win.WS_MAXIMIZEBOX
		win.SetWindowLong(hwnd, win.GWL_STYLE, style)
		win.SetWindowPos(hwnd, 0, 0, 0, 0, 0, win.SWP_NOMOVE|win.SWP_NOSIZE|win.SWP_NOZORDER|win.SWP_FRAMECHANGED)

		centerDialog(dlg, owner, hActive)
	})

	dlg.SizeChanged().Attach(func() {
		centerDialog(dlg, owner, hActive)
	})

	dlg.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		if cfg.AssignTo != nil {
			*cfg.AssignTo = nil
		}
		
		restoreFocus(owner, hActive)
	})

	dlg.Run()
	
	return EditorResult{Accepted: isAccepted, Error: processErr}
}
