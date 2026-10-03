package core

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"mihomo-tray/internal/domain"
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
		return nil, fmt.Errorf("配置合成失败: %w", err)
	}

	runtimeAbs := filepath.Join(baseDir, domain.RuntimeConfigName)

	if existingContent, err := os.ReadFile(runtimeAbs); err == nil {
		if strings.TrimSpace(string(existingContent)) == strings.TrimSpace(string(res.YAML)) {
			slog.Debug("运行时配置无实质变动，跳过落盘与校验")
			return &DeployResult{
				RuntimeAbs:  runtimeAbs,
				TunDevice:   res.TunDevice,
				IsUnchanged: true,
			}, nil
		}
	}

	stageFile, err := os.CreateTemp(baseDir, ".stage_*.yaml")
	if err != nil {
		return nil, fmt.Errorf("创建沙盒测试配置失败: %w", err)
	}
	stagePath := stageFile.Name()

	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(stagePath)
		}
	}()

	if _, err := stageFile.Write(res.YAML); err != nil {
		_ = stageFile.Close()
		return nil, fmt.Errorf("写入沙盒测试配置失败: %w", err)
	}
	_ = stageFile.Sync()
	_ = stageFile.Close()

	exePath := GetKernelPath(baseDir)
	if err := ValidateConfig(exePath, baseDir, stagePath); err != nil {
		return nil, fmt.Errorf("终态配置业务语义错误，内核拒绝加载:\n\n%w", err)
	}

	if err := os.Rename(stagePath, runtimeAbs); err != nil {
		_ = os.Remove(runtimeAbs)
		if err := os.Rename(stagePath, runtimeAbs); err != nil {
			return nil, fmt.Errorf("原子提交正式配置失败: %w", err)
		}
	}
	committed = true

	slog.Debug("已安全提交运行时配置", "target", domain.RuntimeConfigName)
	return &DeployResult{
		RuntimeAbs:  runtimeAbs,
		TunDevice:   res.TunDevice,
		IsUnchanged: false,
	}, nil
}
