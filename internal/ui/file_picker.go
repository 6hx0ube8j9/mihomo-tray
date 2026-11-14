package ui

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/tailscale/walk"
	"github.com/tailscale/win"
)

func RunOpenYAMLFileDialog(owner walk.Form) (string, bool) {
	dlg := new(walk.FileDialog)
	dlg.Title = "导入本地配置"
	dlg.Filter = "YAML 配置文件 (*.yaml;*.yml)|*.yaml;*.yml|所有文件 (*.*)|*.*"
	dlg.Flags = win.OFN_EXPLORER | win.OFN_FILEMUSTEXIST | win.OFN_HIDEREADONLY

	ok, _ := dlg.ShowOpen(owner)
	return dlg.FilePath, ok
}

func RunOpenBrowserExeDialog(owner walk.Form, currentPath string) (string, bool) {
	dlg := new(walk.FileDialog)
	dlg.Title = "选择浏览器可执行文件"
	dlg.Filter = "可执行文件 (*.exe)|*.exe|所有文件 (*.*)|*.*"
	dlg.Flags = win.OFN_EXPLORER | win.OFN_FILEMUSTEXIST | win.OFN_PATHMUSTEXIST | win.OFN_HIDEREADONLY

	if trimmed := strings.TrimSpace(currentPath); trimmed != "" {
		cleanDir := filepath.Clean(filepath.FromSlash(filepath.Dir(trimmed)))
		if fi, err := os.Stat(cleanDir); err == nil && fi.IsDir() {
			dlg.InitialDirPath = cleanDir
		}
	}

	ok, _ := dlg.ShowOpen(owner)
	if !ok {
		return "", false
	}
	return filepath.Clean(filepath.FromSlash(dlg.FilePath)), true
}