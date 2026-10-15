package core

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/fs"
)

type DeployResult struct {
	RuntimeAbs  string
	TunDevice   string
	IsUnchanged bool
}

func sanitizeConfigErr(err error) error {
	if err == nil {
		return nil
	}

	msg := strings.TrimSpace(err.Error())
	msg = strings.TrimPrefix(msg, "yaml: ")
	msg = strings.Replace(msg, ": yaml: ", ": ", 1)

	return errors.New(strings.TrimSpace(msg))
}

func ValidateRuntimeYAML(cfg domain.TrayConfig, sourceBytes []byte, baseDir string) (*ComposeResult, error) {
	res, err := ComposeRuntimeYAML(cfg, sourceBytes)
	if err != nil {
		return nil, sanitizeConfigErr(err)
	}

	testConfigAbs := filepath.Join(baseDir, domain.TestConfigFileName)
	if err := os.WriteFile(testConfigAbs, res.YAML, 0644); err != nil {
		return nil, fmt.Errorf("write test config: %w", err)
	}
	defer os.Remove(testConfigAbs)

	kernelPath := GetKernelPath(baseDir)
	if err := ValidateConfig(kernelPath, baseDir, testConfigAbs); err != nil {
		return nil, sanitizeConfigErr(err)
	}

	return res, nil
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

	runtimeAbs := filepath.Join(baseDir, domain.RuntimeConfigName)

	if existingContent, err := os.ReadFile(runtimeAbs); err == nil {
		res, err := ComposeRuntimeYAML(cfg, sourceBytes)
		if err == nil && bytes.Equal(bytes.TrimSpace(existingContent), bytes.TrimSpace(res.YAML)) {
			slog.Debug("配置内容无变动，跳过校验与落盘")
			return &DeployResult{
				RuntimeAbs:  runtimeAbs,
				TunDevice:   res.TunDevice,
				IsUnchanged: true,
			}, nil
		}
	}

	res, err := ValidateRuntimeYAML(cfg, sourceBytes, baseDir)
	if err != nil {
		return nil, err
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
