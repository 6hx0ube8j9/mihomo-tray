package ui

import (
	"github.com/tailscale/walk"
)

func syncExec(f func()) {
	if globalUIEngine == nil || globalUIEngine.app == nil {
		return
	}
	done := make(chan struct{})
	globalUIEngine.app.Synchronize(func() {
		f()
		close(done)
	})
	<-done
}

func ShowErrorMessage(title, message string) {
	if globalUIEngine == nil {
		return
	}
	syncExec(func() {
		owner := globalUIEngine.getActiveWindow()
		walk.MsgBox(owner, title, message, walk.MsgBoxIconWarning)
	})
}

func ShowConfirmMessage(title, message string) bool {
	if globalUIEngine == nil {
		return false
	}
	var result int
	syncExec(func() {
		owner := globalUIEngine.getActiveWindow()
		result = walk.MsgBox(owner, title, message, walk.MsgBoxIconQuestion|walk.MsgBoxOKCancel)
	})
	return result == walk.DlgCmdOK
}

func OpenYAMLFileDialog() (string, bool) {
	if globalUIEngine == nil {
		return "", false
	}
	var path string
	var accepted bool

	syncExec(func() {
		owner := globalUIEngine.getActiveWindow()
		dlg := new(walk.FileDialog)
		dlg.Title = "选择本地 YAML 配置文件"
		dlg.Filter = "YAML 配置文件 (*.yaml;*.yml)|*.yaml;*.yml|所有文件 (*.*)|*.*"
		
		if ok, _ := dlg.ShowOpen(owner); ok {
			path = dlg.FilePath
			accepted = true
		}
	})
	return path, accepted
}
