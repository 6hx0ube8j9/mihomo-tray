package ui

import (
	"log/slog"
	"path/filepath"

	"github.com/tailscale/walk"

	"mihomo-tray/internal/browser"
	"mihomo-tray/internal/sys"
)

func ownerWindowHandle(owner walk.Form) uintptr {
	if owner != nil {
		return uintptr(owner.Handle())
	}
	return 0
}

func RunOpenYAMLFileDialog(owner walk.Form) (string, bool) {
	selected, ok, err := sys.OpenFileDialog(sys.FileDialogOptions{
		OwnerHWND: ownerWindowHandle(owner),
		Title:     "导入本地配置",
		Filters: []sys.FileFilter{
			{Name: "YAML 配置文件 (*.yaml;*.yml)", Spec: "*.yaml;*.yml"},
			{Name: "所有文件 (*.*)", Spec: "*.*"},
		},
	})
	if err != nil {
		slog.Warn("打开配置文件选择框失败", "err", err)
		return "", false
	}
	if !ok {
		return "", false
	}
	return filepath.Clean(filepath.FromSlash(selected)), true
}

func RunOpenBrowserExeDialog(owner walk.Form, currentPath string) (string, bool) {
	selected, ok, err := sys.OpenFileDialog(sys.FileDialogOptions{
		OwnerHWND:  ownerWindowHandle(owner),
		Title:      "选择文件",
		InitialDir: browser.ResolveNearestDir(currentPath),
		Filters: []sys.FileFilter{
			{Name: "可执行文件 (*.exe)", Spec: "*.exe"},
			{Name: "所有文件 (*.*)", Spec: "*.*"},
		},
	})
	if err != nil {
		slog.Warn("打开浏览器选择框失败", "err", err)
		return "", false
	}
	if !ok {
		return "", false
	}
	return filepath.Clean(filepath.FromSlash(selected)), true
}
