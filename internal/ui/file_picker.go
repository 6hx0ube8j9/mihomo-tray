package ui

import (
	"log/slog"
	"os"
	"path/filepath"

	"github.com/tailscale/walk"

	"mihomo-tray/internal/domain"
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
	var initialDir string
	if resolved := domain.ResolveBrowserPath(currentPath); resolved != "" {
		candidate := filepath.Clean(filepath.FromSlash(resolved))
		if filepath.IsAbs(candidate) {
			if fi, err := os.Stat(candidate); err != nil || !fi.IsDir() {
				candidate = filepath.Dir(candidate)
			}
			for candidate != "" && candidate != "." {
				if fi, err := os.Stat(candidate); err == nil && fi.IsDir() {
					initialDir = candidate
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

	selected, ok, err := sys.OpenFileDialog(sys.FileDialogOptions{
		OwnerHWND:  ownerWindowHandle(owner),
		Title:      "选择文件",
		InitialDir: initialDir,
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