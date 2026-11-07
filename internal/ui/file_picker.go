package ui

import (
	"github.com/tailscale/walk"
)

const (
	ofnExplorer       uint32 = 0x00080000
	ofnEnableSizing   uint32 = 0x00800000
	ofnDontAddToRecent uint32 = 0x02000000
)

func RunOpenYAMLFileDialog(owner walk.Form) (string, bool) {
	dlg := new(walk.FileDialog)
	dlg.Title = "导入本地配置"
	dlg.Filter = "YAML 配置文件 (*.yaml;*.yml)|*.yaml;*.yml|所有文件 (*.*)|*.*"
	
	dlg.Flags = ofnExplorer | ofnEnableSizing | ofnDontAddToRecent

	ok, _ := dlg.ShowOpen(owner)
	return dlg.FilePath, ok
}
