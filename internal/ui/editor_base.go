package ui

import (
	"log/slog"

	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"
	"github.com/tailscale/win"

	"mihomo-tray/internal/sys"
)

type EditorConfig struct {
	Title         string
	Width         int
	MinHeight     int
	Widgets       []Widget
	OnAccept      func(dlg *walk.Dialog) (bool, error)
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
			ok, err := cfg.OnAccept(dlg)
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
		AssignTo:  &dlg,
		Title:     cfg.Title,
		MinSize:   Size{Width: cfg.Width, Height: cfg.MinHeight},
		Font:      Font{Family: "Microsoft YaHei", PointSize: 9},
		Layout:    VBox{Margins: Margins{Left: 15, Top: 12, Right: 15, Bottom: 12}, Spacing: 8},
		Children:  layoutChildren,
		FixedSize: true,
	}.Create(owner)

	if err != nil {
		slog.Error("创建窗口失败", "title", cfg.Title, "err", err)
		return EditorResult{Accepted: false, Error: err}
	}

	if icon, iconErr := walk.NewIconFromResourceId(1); iconErr == nil {
		_ = dlg.SetIcon(icon)
	}

	cleanupKeyFlow := SetupDialogKeyFlow(dlg, acceptPB, cancelPB)
	defer func() {
		cleanupKeyFlow()
		dlg.Dispose()
	}()

	if cfg.OnReady != nil {
		cfg.OnReady(dlg)
	}

	center := func() {
		var ownerHwnd uintptr
		if owner != nil && owner.Visible() {
			ownerHwnd = uintptr(owner.Handle())
		}
		sys.CenterWindowOnOwner(uintptr(dlg.Handle()), ownerHwnd)
	}

	dlg.Starting().Attach(func() {
		walk.App().Synchronize(center)
	})

	// With FixedSize: true, walk may defer final height calculation (e.g. VSpacer)
	// past Starting(); re-centering on SizeChanged prevents a downward visual offset.
	dlg.SizeChanged().Attach(center)

	dlg.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		if owner != nil && owner.Visible() {
			sys.ActivateWindow(uintptr(owner.Handle()))
			return
		}
		sys.ActivateWindow(uintptr(hActive))
	})

	dlg.Run()

	return EditorResult{Accepted: isAccepted, Error: processErr}
}
