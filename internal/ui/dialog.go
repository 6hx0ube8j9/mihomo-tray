package ui

import (
	"github.com/tailscale/walk"
)

func getOwner() walk.Form {
	if globalUIEngine != nil {
		if globalUIEngine.panelWindow != nil && globalUIEngine.panelWindow.Visible() {
			return globalUIEngine.panelWindow
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
		
		ok, _ := dlg.ShowOpen(getOwner())
		resultCh <- fileResult{Path: dlg.FilePath, OK: ok}
	})
	
	res := <-resultCh
	return res.Path, res.OK
}

func ShowErrorMessage(owner walk.Form, title, message string) {
	if globalUIEngine == nil || globalUIEngine.app == nil {
		return
	}
	
	globalUIEngine.app.Synchronize(func() {
		parent := owner
		if parent == nil {
			parent = getOwner()
		}
		walk.MsgBox(parent, title, message, walk.MsgBoxIconWarning)
	})
}

func ShowConfirmMessage(owner walk.Form, title, message string) bool {
	if globalUIEngine == nil || globalUIEngine.app == nil {
		return false
	}
	
	resultCh := make(chan int)
	
	globalUIEngine.app.Synchronize(func() {
		parent := owner
		if parent == nil {
			parent = getOwner()
		}
		res := walk.MsgBox(parent, title, message, walk.MsgBoxIconQuestion|walk.MsgBoxOKCancel)
		resultCh <- res
	})
	
	return <-resultCh == walk.DlgCmdOK
}
