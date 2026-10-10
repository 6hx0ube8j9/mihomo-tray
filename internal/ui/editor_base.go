package ui

import (
	"log/slog"

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
	OnReady       func(dlg *walk.Dialog)
}

type EditorResult struct {
	Accepted bool
	Error    error
}

func RunEditor(owner walk.Form, cfg EditorConfig) EditorResult {
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

	submit := func() {
		if cfg.OnAccept != nil {
			ok, err := cfg.OnAccept()
			if !ok {
				return
			}
			processErr = err
		}
		isAccepted = true
		dlg.Accept()
	}

	btnSize := Size{Width: 80, Height: 26}
	layoutChildren := make([]Widget, 0, len(cfg.Widgets)+2)
	layoutChildren = append(layoutChildren, cfg.Widgets...)
	layoutChildren = append(layoutChildren,
		VSpacer{},
		Composite{
			Layout: HBox{MarginsZero: true, Spacing: 10},
			Children: []Widget{
				HSpacer{},
				PushButton{
					AssignTo:  &acceptPB,
					Text:      cfg.AcceptBtnText,
					MinSize:   btnSize,
					OnClicked: submit,
				},
				PushButton{
					AssignTo:  &cancelPB,
					Text:      cfg.CancelBtnText,
					MinSize:   btnSize,
					OnClicked: func() { dlg.Cancel() },
				},
			},
		},
	)

	err := Dialog{
		AssignTo: &dlg,
		Title:    cfg.Title,
		MinSize:  Size{Width: cfg.Width, Height: cfg.MinHeight},
		Layout:   VBox{Margins: Margins{Left: 18, Top: 15, Right: 18, Bottom: 15}, Spacing: 12},
		Children: layoutChildren,
	}.Create(owner)

	if err != nil {
		slog.Error("模态编辑器窗口创建失败", "title", cfg.Title, "err", err)
		return EditorResult{Accepted: false, Error: err}
	}

	cleanupKeyFlow := SetupDialogKeyFlow(dlg, acceptPB, cancelPB)
	defer func() {
		cleanupKeyFlow()
		dlg.Dispose()
	}()

	if cfg.OnReady != nil {
		cfg.OnReady(dlg)
	}

	dlg.Starting().Attach(func() {
		lockWindowSize(dlg.Handle())
		centerDialog(dlg, owner)
	})

	dlg.SizeChanged().Attach(func() {
		centerDialog(dlg, owner)
	})

	dlg.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		restoreFocus(owner, hActive)
	})

	dlg.Run()

	return EditorResult{Accepted: isAccepted, Error: processErr}
}
