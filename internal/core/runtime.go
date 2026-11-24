package core

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/fs"
)

func ValidateRuntimeYAML(cfg domain.TrayConfig, sourceBytes []byte, baseDir string) (*ComposeResult, error) {
	res, err := ComposeRuntimeYAML(cfg, sourceBytes)
	if err != nil {
		return nil, err
	}

	if err := ValidateConfig(GetKernelPath(baseDir), baseDir, res.YAML); err != nil {
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
