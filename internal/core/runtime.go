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

	tmpFile, err := os.CreateTemp(baseDir, ".check-*.yaml")
	if err != nil {
		return nil, fmt.Errorf("create test config: %w", err)
	}
	testConfigAbs := tmpFile.Name()

	if _, err := tmpFile.Write(res.YAML); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(testConfigAbs)
		return nil, fmt.Errorf("write test config: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(testConfigAbs)
		return nil, fmt.Errorf("close test config: %w", err)
	}

	kernelPath := GetKernelPath(baseDir)
	if err := ValidateConfig(kernelPath, baseDir, testConfigAbs, true); err != nil {
		return nil, err
	}

	return res, nil
}

func PrepareRuntimeYAML(cfg domain.TrayConfig, relPath string, baseDir string) (*ComposeResult, error) {
	var sourceBytes []byte
	if relPath != "" {
		sourcePath := filepath.Join(baseDir, filepath.FromSlash(relPath))
		content, err := os.ReadFile(sourcePath)
		if err != nil {
			return nil, fmt.Errorf("read file: %w", err)
		}
		sourceBytes = content
	}

	return ComposeRuntimeYAML(cfg, sourceBytes)
}

func WriteRuntimeYAML(yamlBytes []byte, baseDir string) (string, bool, error) {
	runtimeAbs := filepath.Join(baseDir, domain.RuntimeConfigName)
	if existingContent, err := os.ReadFile(runtimeAbs); err == nil &&
		bytes.Equal(bytes.TrimSpace(existingContent), bytes.TrimSpace(yamlBytes)) {
		return runtimeAbs, true, nil
	}

	if err := fs.WriteAtomic(runtimeAbs, yamlBytes); err != nil {
		return "", false, fmt.Errorf("write runtime config: %w", err)
	}

	return runtimeAbs, false, nil
}

func DeployRuntimeConfig(cfg domain.TrayConfig, relPath string, baseDir string) (*DeployResult, error) {
	res, err := PrepareRuntimeYAML(cfg, relPath, baseDir)
	if err != nil {
		return nil, err
	}

	runtimeAbs, unchanged, err := WriteRuntimeYAML(res.YAML, baseDir)
	if err != nil {
		return nil, err
	}

	return &DeployResult{
		RuntimeAbs:  runtimeAbs,
		TunDevice:   res.TunDevice,
		IsUnchanged: unchanged,
	}, nil
}

func CleanupTempCheckFiles(baseDir string) {
	matches, err := filepath.Glob(filepath.Join(baseDir, ".check-*.yaml"))
	if err != nil {
		return
	}
	for _, path := range matches {
		_ = os.Remove(path)
	}
}
