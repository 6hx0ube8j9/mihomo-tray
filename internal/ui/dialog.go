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

// OpenYAMLFileDialog 打开本地 YAML 配置文件选择器 (补回误删的方法)
func OpenYAMLFileDialog() (string, bool) {
	if GlobalEngine == nil || GlobalEngine.app == nil {
		return "", false
	}
	type fileResult struct {
		Path string
		OK   bool
	}
	resultCh := make(chan fileResult)

	GlobalEngine.app.Synchronize(func() {
		dlg := new(walk.FileDialog)
		dlg.Title = "选择本地 YAML 配置文件"
		dlg.Filter = "YAML 配置文件 (*.yaml;*.yml)|*.yaml;*.yml|所有文件 (*.*)|*.*"
		ok, _ := dlg.ShowOpen(getValidOwner())
		resultCh <- fileResult{Path: dlg.FilePath, OK: ok}
	})
	res := <-resultCh
	return res.Path, res.OK
}

func ShowErrorMessage(owner walk.Form, title, message string) {
	if GlobalEngine != nil && GlobalEngine.app != nil {
		GlobalEngine.app.Synchronize(func() {
			parent := owner
			if parent == nil {
				parent = getValidOwner()
			}
			walk.MsgBox(parent, title, message, walk.MsgBoxIconError)
		})
	}
}

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

func ShowInfoModeless(owner walk.Form, title, message string) {
	ShowInfoMessage(owner, title, message)
}
