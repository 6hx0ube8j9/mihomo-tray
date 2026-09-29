package ui

import (
	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"
	"github.com/tailscale/win"
)

type EditorConfig struct {
	Title         string
	Width         int
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
	if GlobalEngine == nil || GlobalEngine.app == nil {
		return EditorResult{Accepted: false}
	}

	if cfg.AcceptBtnText == "" {
		cfg.AcceptBtnText = "保存"
	}
	if cfg.CancelBtnText == "" {
		cfg.CancelBtnText = "取消"
	}

	resultCh := make(chan EditorResult)

	GlobalEngine.app.Synchronize(func() {
		parent := owner
		if parent == nil {
			parent = getValidOwner()
		}
		hActive := win.GetForegroundWindow()

		var dlg *walk.Dialog
		var acceptPB, cancelPB *walk.PushButton

		layoutChildren := append(cfg.Widgets,
			VSpacer{},
			Composite{
				Layout: HBox{MarginsZero: true, Spacing: 10},
				Children: []Widget{
					HSpacer{},
					PushButton{
						AssignTo: &acceptPB,
						Text:     cfg.AcceptBtnText,
						MinSize:  Size{Width: 90, Height: 26},
						OnClicked: func() {
							if cfg.OnAccept != nil {
								ok, err := cfg.OnAccept()
								if !ok {
									return
								}
								resultCh <- EditorResult{Accepted: true, Error: err}
							} else {
								resultCh <- EditorResult{Accepted: true}
							}
							dlg.Accept()
						},
					},
					PushButton{
						AssignTo: &cancelPB,
						Text:     cfg.CancelBtnText,
						MinSize:  Size{Width: 90, Height: 26},
						OnClicked: func() {
							resultCh <- EditorResult{Accepted: false}
							dlg.Cancel()
						},
					},
				},
			},
		)

		err := Dialog{
			AssignTo:      &dlg,
			Title:         cfg.Title,
			MinSize:       Size{Width: cfg.Width, Height: 0},
			Layout:        VBox{Margins: Margins{Left: 15, Top: 15, Right: 15, Bottom: 15}, Spacing: 12},
			DefaultButton: &acceptPB,
			CancelButton:  &cancelPB,
			Children:      layoutChildren,
		}.Create(parent)

		if err != nil {
			resultCh <- EditorResult{Accepted: false, Error: err}
			return
		}

		defer dlg.Dispose()

		dlg.Starting().Attach(func() {
			centerDialog(dlg, parent, hActive)
		})

		dlg.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
			if parent != nil && parent.Visible() && !win.IsIconic(parent.Handle()) {
				win.SetForegroundWindow(parent.Handle())
				win.SetFocus(parent.Handle())
			} else if hActive != 0 && win.IsWindowVisible(hActive) && !win.IsIconic(hActive) {
				win.SetForegroundWindow(hActive)
				win.SetFocus(hActive)
			}
		})

		if dlg.Run() != win.IDOK {
			select {
			case resultCh <- EditorResult{Accepted: false}:
			default:
			}
		}
	})

	return <-resultCh
}
