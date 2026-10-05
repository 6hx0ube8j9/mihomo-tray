package core

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/fs"
)

type DeployResult struct {
	RuntimeAbs  string
	TunDevice   string
	IsUnchanged bool
}

func DeployRuntimeConfig(cfg domain.TrayConfig, relPath string, baseDir string) (*DeployResult, error) {
	var sourceBytes []byte
	if relPath != "" {
		sourcePath := filepath.Join(baseDir, filepath.FromSlash(relPath))
		content, err := os.ReadFile(sourcePath)
		if err != nil {
			return nil, fmt.Errorf("读取基础配置文件失败: %w", err)
		}
		sourceBytes = content
	}

	res, err := ComposeRuntimeYAML(cfg, sourceBytes)
	if err != nil {
		return nil, fmt.Errorf("生成运行配置失败: %w", err)
	}

	runtimeAbs := filepath.Join(baseDir, domain.RuntimeConfigName)

	if existingContent, err := os.ReadFile(runtimeAbs); err == nil {
		if bytes.Equal(bytes.TrimSpace(existingContent), bytes.TrimSpace(res.YAML)) {
			slog.Debug("配置内容无变动，跳过落盘操作")
			return &DeployResult{
				RuntimeAbs:  runtimeAbs,
				TunDevice:   res.TunDevice,
				IsUnchanged: true,
			}, nil
		}
	}

	if err := fs.WriteAtomic(runtimeAbs, res.YAML); err != nil {
		return nil, fmt.Errorf("保存运行配置文件失败: %w", err)
	}

	slog.Debug("运行配置已更新落盘", "target", domain.RuntimeConfigName)
	return &DeployResult{
		RuntimeAbs:  runtimeAbs,
		TunDevice:   res.TunDevice,
		IsUnchanged: false,
	}, nil
}
