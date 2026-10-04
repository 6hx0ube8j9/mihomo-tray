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
			return nil, fmt.Errorf("底稿文件读取失败: %w", err)
		}
		sourceBytes = content
	}

	res, err := ComposeRuntimeYAML(cfg, sourceBytes)
	if err != nil {
		return nil, fmt.Errorf("配置语法合成失败: %w", err)
	}

	runtimeAbs := filepath.Join(baseDir, domain.RuntimeConfigName)

	if existingContent, err := os.ReadFile(runtimeAbs); err == nil {
		if bytes.Equal(bytes.TrimSpace(existingContent), bytes.TrimSpace(res.YAML)) {
			slog.Debug("运行时配置无变动，跳过落盘")
			return &DeployResult{RuntimeAbs: runtimeAbs, TunDevice: res.TunDevice, IsUnchanged: true}, nil
		}
	}

	if err := fs.WriteAtomic(runtimeAbs, res.YAML); err != nil {
		return nil, fmt.Errorf("提交正式配置失败: %w", err)
	}

	slog.Debug("已安全提交运行时配置", "target", domain.RuntimeConfigName)
	return &DeployResult{RuntimeAbs: runtimeAbs, TunDevice: res.TunDevice, IsUnchanged: false}, nil
}

func writeStageConfig(baseDir string, data []byte) (string, error) {
	stageFile, err := os.CreateTemp(baseDir, ".stage_*.yaml")
	if err != nil {
		return "", fmt.Errorf("创建沙盒测试配置失败: %w", err)
	}
	stagePath := stageFile.Name()

	var writeSucceeded bool
	defer func() {
		if !writeSucceeded {
			_ = stageFile.Close()
			_ = os.Remove(stagePath)
		}
	}()

	if _, err := stageFile.Write(data); err != nil {
		return "", fmt.Errorf("写入沙盒测试配置失败: %w", err)
	}
	if err := stageFile.Sync(); err != nil {
		return "", fmt.Errorf("沙盒测试配置刷盘失败: %w", err)
	}
	if err := stageFile.Close(); err != nil {
		return "", fmt.Errorf("关闭沙盒测试配置句柄失败: %w", err)
	}

	writeSucceeded = true
	return stagePath, nil
}
