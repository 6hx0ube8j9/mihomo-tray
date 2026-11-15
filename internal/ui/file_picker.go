package ui

import (
	"os"
	"path/filepath"

	"github.com/tailscale/walk"
	"github.com/tailscale/win"

	"mihomo-tray/internal/domain"
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

	if resolved := domain.ResolveBrowserPath(currentPath); resolved != "" {
		candidate := filepath.Clean(filepath.FromSlash(resolved))
		if filepath.IsAbs(candidate) {
			if fi, err := os.Stat(candidate); err != nil || !fi.IsDir() {
				candidate = filepath.Dir(candidate)
			}
			for candidate != "" && candidate != "." {
				if fi, err := os.Stat(candidate); err == nil && fi.IsDir() {
					dlg.InitialDirPath = candidate
					break
				}
				parent := filepath.Dir(candidate)
				if parent == candidate {
					break
				}
				candidate = parent
			}
		}
	}

	ok, _ := dlg.ShowOpen(owner)
	if !ok {
		return "", false
	}
	return filepath.Clean(filepath.FromSlash(dlg.FilePath)), true
}