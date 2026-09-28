package ui

import (
	"github.com/tailscale/walk"
	"github.com/tailscale/win"
)

// getValidOwner 自动寻找当前活跃的父窗口，确保弹窗有归属
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

// ShowErrorMessage 弹出包含错误图标的原生提示框
func ShowErrorMessage(owner walk.Form, title, message string) {
	if GlobalEngine != nil && GlobalEngine.app != nil {
		GlobalEngine.app.Synchronize(func() {
			parent := owner
			if parent == nil {
				parent = getValidOwner()
			}
			// 调用原生 MessageBox，系统级死锁焦点，绝对防抖
			walk.MsgBox(parent, title, message, walk.MsgBoxIconError)
		})
	}
}

// ShowInfoMessage 弹出包含信息图标的原生提示框
func ShowInfoMessage(owner walk.Form, title, message string) {
	if GlobalEngine != nil && GlobalEngine.app != nil {
		GlobalEngine.app.Synchronize(func() {
			parent := owner
			if parent == nil {
				parent = getValidOwner()
			}
			walk.MsgBox(parent, title, message, walk.MsgBoxIconInformation)
		})
	}
}

// ShowConfirmMessage 弹出包含问号图标的确认框，返回布尔值
func ShowConfirmMessage(owner walk.Form, title, message string) bool {
	if GlobalEngine == nil || GlobalEngine.app == nil {
		return false
	}
	resultCh := make(chan bool)
	GlobalEngine.app.Synchronize(func() {
		parent := owner
		if parent == nil {
			parent = getValidOwner()
		}
		res := walk.MsgBox(parent, title, message, walk.MsgBoxIconQuestion|walk.MsgBoxYesNo)
		resultCh <- res == walk.DlgCmdYes
	})
	return <-resultCh
}

// 兼容旧版调用的别名
func ShowInfoModeless(owner walk.Form, title, message string) { 
	ShowInfoMessage(owner, title, message) 
}
