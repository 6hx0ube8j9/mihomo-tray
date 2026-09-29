package ui

import (
	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"
	"github.com/tailscale/win"
)

// EditorConfig 是所有表单编辑器的配置清单
type EditorConfig struct {
	Title         string               // 弹窗标题
	Width         int                  // 建议的宽度 (Height 会根据内容自动适应)
	Widgets       []Widget             // 表单核心内容区
	OnAccept      func() (bool, error) // 点击“保存”时的验证回调，返回 true 表示验证通过并允许关闭
	AcceptBtnText string               // 确定按钮文字 (默认"保存")
	CancelBtnText string               // 取消按钮文字 (默认"取消")
}

// EditorResult 是通用的返回值
type EditorResult struct {
	Accepted bool
	Error    error
}

// RunEditor 是驱动所有表单编辑器的中央枢纽
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
			parent = getValidOwner() // 完美复用 alert.go 里的函数
		}
		hActive := win.GetForegroundWindow()

		var dlg *walk.Dialog
		var acceptPB, cancelPB *walk.PushButton

		// 将用户自定义的控件与底部的按钮拼接起来
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
							// 调用子类的验证逻辑
							if cfg.OnAccept != nil {
								ok, err := cfg.OnAccept()
								if !ok {
									// 验证失败，可以在子类里弹错误提示，这里阻止窗口关闭
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
			centerDialog(dlg, parent, hActive) // 完美复用 alert.go 里的居中算法
		})

		dlg.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
			// 焦点恢复逻辑
			if parent != nil && parent.Visible() && !win.IsIconic(parent.Handle()) {
				win.SetForegroundWindow(parent.Handle())
				win.SetFocus(parent.Handle())
			} else if hActive != 0 && win.IsWindowVisible(hActive) && !win.IsIconic(hActive) {
				win.SetForegroundWindow(hActive)
				win.SetFocus(hActive)
			}
		})

		// 如果用户点了右上角的 X 关闭窗口，必须返回 false
		if dlg.Run() != win.IDOK {
			select {
			case resultCh <- EditorResult{Accepted: false}:
			default:
			}
		}
	})

	return <-resultCh
}
