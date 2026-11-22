package core

import (
	"bytes"
	"fmt"
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

func ValidateRuntimeYAML(cfg domain.TrayConfig, sourceBytes []byte, baseDir string) (*ComposeResult, error) {
	res, err := ComposeRuntimeYAML(cfg, sourceBytes)
	if err != nil {
		return nil, err
	}

	if err := validateComposedYAML(res.YAML, baseDir); err != nil {
		return nil, err
	}

	return res, nil
}

func validateComposedYAML(yamlBytes []byte, baseDir string) error {
	testConfigAbs := filepath.Join(baseDir, domain.TestConfigFileName)
	if err := os.WriteFile(testConfigAbs, yamlBytes, 0644); err != nil {
		return fmt.Errorf("write test config: %w", err)
	}
	defer os.Remove(testConfigAbs)

	kernelPath := GetKernelPath(baseDir)
	return ValidateConfig(kernelPath, baseDir, testConfigAbs)
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
		return nil, err
	}

	runtimeAbs := filepath.Join(baseDir, domain.RuntimeConfigName)
	if existingContent, err := os.ReadFile(runtimeAbs); err == nil &&
		bytes.Equal(bytes.TrimSpace(existingContent), bytes.TrimSpace(res.YAML)) {
		return &DeployResult{
			RuntimeAbs:  runtimeAbs,
			TunDevice:   res.TunDevice,
			IsUnchanged: true,
		}, nil
	}

	if err := validateComposedYAML(res.YAML, baseDir); err != nil {
		return nil, err
	}

	if err := fs.WriteAtomic(runtimeAbs, res.YAML); err != nil {
		return nil, fmt.Errorf("write runtime config: %w", err)
	}

	return &DeployResult{
		RuntimeAbs:  runtimeAbs,
		TunDevice:   res.TunDevice,
		IsUnchanged: false,
	}, nil
}
