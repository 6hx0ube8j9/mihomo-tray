package ui

import (
	"github.com/tailscale/walk"
)

func RunOpenYAMLFileDialog(owner walk.Form) (string, bool) {
	dlg := new(walk.FileDialog)
	dlg.Title = "导入本地配置"
	dlg.Filter = "YAML 配置文件 (*.yaml;*.yml)|*.yaml;*.yml|所有文件 (*.*)|*.*"
	
	ok, _ := dlg.ShowOpen(owner)
	return dlg.FilePath, ok
}
