package ui

import (
	"strings"
	"unsafe"

	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"
	"github.com/tailscale/win"
)

const spiGetWorkArea = 0x0030

// ==================== 业务调用接口 ====================

// ShowConfirmDialog 确认提示弹窗（供外部调用：使用问号图标 + 播放清脆提示音，返回是否点击确定）
func ShowConfirmDialog(owner walk.Form, title, message string) bool {
	return RunConfirmDialog(owner, title, message)
}

// ShowErrorDialog 错误提示弹窗（供外部调用：使用错误红叉图标 + 播放错误音）
func ShowErrorDialog(owner walk.Form, title, message string) {
	RunErrorDialog(owner, title, message)
}

// RunConfirmDialog 确认弹窗（保持问号图标，声音使用 MB_ICONEXCLAMATION 确保能响）
func RunConfirmDialog(owner walk.Form, title, message string) bool {
	return runBaseDialog(
		owner,
		title,
		message,
		walk.IconQuestion(),    // 保持问号图标
		win.MB_ICONEXCLAMATION, // 绑定有声的系统提示音（解决 Windows 10/11 问号静音问题）
		true,                   // 显示“确定”与“取消”双按钮
		nil,
	)
}

// RunErrorDialog 错误弹窗（红叉图标 + 严重停止错误音）
func RunErrorDialog(owner walk.Form, title, message string) {
	RunAlertDialog(owner, title, message, walk.IconError(), win.MB_ICONERROR)
}

// RunAlertDialog 通用单按钮提示弹窗
func RunAlertDialog(owner walk.Form, title, message string, icon *walk.Icon, beep uint32) {
	runBaseDialog(owner, title, message, icon, beep, false, nil)
}

// ==================== 基础弹窗底层引擎 ====================

func runBaseDialog(owner walk.Form, title, message string, icon *walk.Icon, beep uint32, isConfirm bool, onReady func(dlg *walk.Dialog)) bool {
	parent := owner
	hActive := win.GetForegroundWindow()
	safeMsg := autoWrapText(message, 55)

	var dlg *walk.Dialog
	var acceptPB, cancelPB *walk.PushButton
	accepted := false

	// 构建底部操作按钮
	buttons := []Widget{
		HSpacer{},
		PushButton{
			AssignTo: &acceptPB,
			Text:     "确定",
			MinSize:  Size{Width: 80, Height: 26},
			OnClicked: func() {
				accepted = true
				dlg.Accept()
			},
		},
	}

	if isConfirm {
		buttons = append(buttons, PushButton{
			AssignTo: &cancelPB,
			Text:     "取消",
			MinSize:  Size{Width: 80, Height: 26},
			OnClicked: func() {
				accepted = false
				dlg.Cancel()
			},
		})
	}

	dlgConfig := Dialog{
		AssignTo: &dlg,
		Title:    title,
		// 1. 修复按钮失效核心：放开高度限制，预留足够客户区容纳文本与按钮
		MinSize:       Size{Width: 320, Height: 155},
		Layout:        VBox{Margins: Margins{Left: 15, Top: 15, Right: 15, Bottom: 12}, Spacing: 12},
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
				Layout:   HBox{MarginsZero: true, Spacing: 10},
				Children: buttons,
			},
		},
	}

	// 统一处理 Esc/关闭键逻辑
	if isConfirm {
		dlgConfig.CancelButton = &cancelPB
	} else {
		dlgConfig.CancelButton = &acceptPB
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

		// 2. 将焦点默认设置到“确定”按键上
		if acceptPB != nil {
			acceptPB.SetFocus()
		}
	})

	dlg.SizeChanged().Attach(func() {
		centerDialog(dlg, parent, hActive)
	})

	dlg.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		restoreFocus(parent, hActive)
	})

	// 3. 运行对话框，双重校验（无论是鼠标点击还是回车键触发都判定为通过）
	cmd := dlg.Run()
	return cmd == walk.DlgCmdOK || accepted
}

// ==================== 辅助方法 ====================

// autoWrapText 自动文本折行，防止单行过长撑爆窗口
func autoWrapText(s string, limit int) string {
	if limit <= 0 || len(s) <= limit {
		return s
	}
	var b strings.Builder
	count := 0
	for _, r := range s {
		if r == '\n' {
			count = 0
			b.WriteRune(r)
			continue
		}
		if count >= limit {
			b.WriteRune('\n')
			count = 0
		}
		b.WriteRune(r)
		count++
	}
	return b.String()
}

// lockWindowSize 禁止用户手动拉伸弹窗边缘
func lockWindowSize(hwnd win.HWND) {
	style := win.GetWindowLong(hwnd, win.GWL_STYLE)
	style &^= (win.WS_THICKFRAME | win.WS_MAXIMIZEBOX)
	win.SetWindowLong(hwnd, win.GWL_STYLE, style)
}

// centerDialog 弹窗居中显示（优先相对父窗口居中，无父窗口则相对当前屏幕居中并置顶）
func centerDialog(dlg *walk.Dialog, parent walk.Form, hActive win.HWND) {
	var rect win.RECT
	win.GetWindowRect(dlg.Handle(), &rect)
	dlgW := rect.Right - rect.Left
	dlgH := rect.Bottom - rect.Top

	var screenW, screenH, workLeft, workTop int32
	var workArea win.RECT
	if win.SystemParametersInfo(spiGetWorkArea, 0, unsafe.Pointer(&workArea), 0) {
		screenW = workArea.Right - workArea.Left
		screenH = workArea.Bottom - workArea.Top
		workLeft = workArea.Left
		workTop = workArea.Top
	} else {
		screenW = win.GetSystemMetrics(win.SM_CXSCREEN)
		screenH = win.GetSystemMetrics(win.SM_CYSCREEN)
	}

	var x, y int32
	if parent != nil && parent.Visible() {
		var pRect win.RECT
		win.GetWindowRect(parent.Handle(), &pRect)
		x = pRect.Left + (pRect.Right-pRect.Left-dlgW)/2
		y = pRect.Top + (pRect.Bottom-pRect.Top-dlgH)/2
	} else {
		x = workLeft + (screenW-dlgW)/2
		y = workTop + (screenH-dlgH)/2
	}

	win.SetWindowPos(dlg.Handle(), win.HWND_TOP, x, y, 0, 0, win.SWP_NOSIZE)
	win.SetForegroundWindow(dlg.Handle())
}

// restoreFocus 弹窗关闭后将前台焦点还给原来的窗口
func restoreFocus(parent walk.Form, hActive win.HWND) {
	if parent != nil && parent.Visible() {
		win.SetForegroundWindow(parent.Handle())
	} else if hActive != 0 {
		win.SetForegroundWindow(hActive)
	}
}
