package ui

import (
	"github.com/tailscale/walk"
	"github.com/tailscale/win"
)

func getValidOwner() walk.Form {
	if globalUIEngine != nil {
		if globalUIEngine.dashboardWindow != nil {
			hwnd := globalUIEngine.dashboardWindow.Handle()
			if win.IsWindowVisible(hwnd) && !win.IsIconic(hwnd) {
				return globalUIEngine.dashboardWindow
			}
		}
		if globalUIEngine.mw != nil {
			return globalUIEngine.mw
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

		ok, _ := dlg.ShowOpen(getValidOwner())
		resultCh <- fileResult{Path: dlg.FilePath, OK: ok}
	})

	res := <-resultCh
	return res.Path, res.OK
}

func RunErrorDialog(owner walk.Form, title, message string) {
	parent := owner
	if parent == nil {
		parent = getValidOwner()
	}
	
	walk.MsgBox(parent, title, message, walk.MsgBoxIconError|walk.MsgBoxOK)
}

func ShowErrorMessage(owner walk.Form, title, message string) {
	if globalUIEngine == nil || globalUIEngine.app == nil {
		return
	}
	globalUIEngine.app.Synchronize(func() {
		RunErrorDialog(owner, title, message)
	})
}

func RunConfirmDialog(owner walk.Form, title, message string) bool {
	parent := owner
	if parent == nil {
		parent = getValidOwner()
	}

	result := walk.MsgBox(parent, title, message, walk.MsgBoxIconQuestion|walk.MsgBoxOKCancel)
	return result == walk.DlgCmdOK
}

func ShowConfirmMessage(owner walk.Form, title, message string) bool {
	if globalUIEngine == nil || globalUIEngine.app == nil {
		return false
	}

	resultCh := make(chan bool)
	globalUIEngine.app.Synchronize(func() {
		res := RunConfirmDialog(owner, title, message)
		resultCh <- res
	})
	return <-resultCh
}
