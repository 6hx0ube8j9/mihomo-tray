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
			return nil, fmt.Errorf("read file: %w", err)
		}
		sourceBytes = content
	}

	res, err := ComposeRuntimeYAML(cfg, sourceBytes)
	if err != nil {
		return nil, fmt.Errorf("compose yaml: %w", err)
	}

	runtimeAbs := filepath.Join(baseDir, domain.RuntimeConfigName)

	if existingContent, err := os.ReadFile(runtimeAbs); err == nil {
		if bytes.Equal(bytes.TrimSpace(existingContent), bytes.TrimSpace(res.YAML)) {
			slog.Debug("配置内容无变动，跳过校验与落盘")
			return &DeployResult{
				RuntimeAbs:  runtimeAbs,
				TunDevice:   res.TunDevice,
				IsUnchanged: true,
			}, nil
		}
	}

	testConfigAbs := filepath.Join(baseDir, domain.TestConfigFileName)
	if err := os.WriteFile(testConfigAbs, res.YAML, 0644); err != nil {
		return nil, fmt.Errorf("write test config: %w", err)
	}
	defer os.Remove(testConfigAbs)

	kernelPath := GetKernelPath(baseDir)
	if err := ValidateConfig(kernelPath, baseDir, testConfigAbs); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}

	if err := fs.WriteAtomic(runtimeAbs, res.YAML); err != nil {
		return nil, fmt.Errorf("write runtime config: %w", err)
	}

	slog.Debug("运行配置已更新落盘", "target", domain.RuntimeConfigName)
	return &DeployResult{
		RuntimeAbs:  runtimeAbs,
		TunDevice:   res.TunDevice,
		IsUnchanged: false,
	}, nil
}
