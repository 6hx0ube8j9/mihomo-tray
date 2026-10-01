package ui

import (
	"github.com/tailscale/walk"
)

func OpenYAMLFileDialog() (string, bool) {
	if GlobalEngine == nil || GlobalEngine.app == nil {
		return "", false
	}
	
	type fileResult struct {
		Path string
		OK   bool
	}
	resultCh := make(chan fileResult, 1)

	safeSync(func() {
		dlg := new(walk.FileDialog)
		dlg.Title = "导入本地配置"
		dlg.Filter = "YAML 配置文件 (*.yaml;*.yml)|*.yaml;*.yml|所有文件 (*.*)|*.*"
		
		ok, _ := dlg.ShowOpen(getValidOwner())
		resultCh <- fileResult{Path: dlg.FilePath, OK: ok}
	})
	
	res := <-resultCh
	return res.Path, res.OK
}
