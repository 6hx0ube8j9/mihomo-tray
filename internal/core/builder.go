package core

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"mihomo-tray/internal/domain"
)

const yamlHeader = "# Auto-generated runtime configuration. DO NOT EDIT.\n\n"

func BuildRuntimeYAML(cfg domain.TrayConfig, relPath string, baseDir string) (bool, map[string]string, error) {
	var data map[string]any
	extracted := make(map[string]string)

	if relPath != "" {
		sourcePath := filepath.Join(baseDir, filepath.FromSlash(relPath))
		content, err := os.ReadFile(sourcePath)
		if err != nil {
			return false, nil, fmt.Errorf("底稿文件读取失败: %w", err)
		}
		if err := yaml.Unmarshal(content, &data); err != nil {
			return false, nil, fmt.Errorf("底稿 YAML 格式错误: %w", err)
		}
	}

	if data == nil {
		slog.Info("当前无活跃配置，将生成极简保底运行参数")
		data = make(map[string]any)
	}

	delete(data, "mixed-port")
	delete(data, "port")
	delete(data, "socks-port")
	delete(data, "redir-port")
	delete(data, "tproxy-port")
	
	delete(data, "external-controller")
	delete(data, "secret")
	delete(data, "external-ui")
	delete(data, "external-ui-url")
	delete(data, "external-ui-name")
	delete(data, "external-controller-pipe")
	delete(data, "external-controller-cors")

	if cfg.Config.MixedPort != nil && *cfg.Config.MixedPort > 0 { 
		data["mixed-port"] = *cfg.Config.MixedPort 
		extracted["port"] = strconv.Itoa(*cfg.Config.MixedPort)
	} else {
		extracted["port"] = strconv.Itoa(domain.DefaultMixedPort)
	}
	if cfg.Config.Port != nil && *cfg.Config.Port > 0 { data["port"] = *cfg.Config.Port }
	if cfg.Config.SocksPort != nil && *cfg.Config.SocksPort > 0 { data["socks-port"] = *cfg.Config.SocksPort }
	if cfg.Config.Mode != "" { data["mode"] = cfg.Config.Mode }
	if cfg.Config.LogLevel != "" { data["log-level"] = cfg.Config.LogLevel }
	if cfg.Config.AllowLan != nil { data["allow-lan"] = *cfg.Config.AllowLan }
	if cfg.Config.UnifiedDelay != nil { data["unified-delay"] = *cfg.Config.UnifiedDelay }
	if cfg.Config.ExternalController != "" { data["external-controller"] = cfg.Config.ExternalController }
	if cfg.Config.Secret != "" { data["secret"] = cfg.Config.Secret }
	if cfg.Config.ExternalUI != "" { data["external-ui"] = cfg.Config.ExternalUI }
	if cfg.Config.ExternalUIURL != "" { data["external-ui-url"] = cfg.Config.ExternalUIURL }
	
	if cfg.Config.ExternalUIName != "" { data["external-ui-name"] = cfg.Config.ExternalUIName }
	extracted["external-ui-name"] = cfg.Config.ExternalUIName

	if cfg.Config.ExternalControllerPipe != "" { data["external-controller-pipe"] = cfg.Config.ExternalControllerPipe }

	corsMap := map[string]any{
		"allow-origins": cfg.Config.ExternalControllerCors.AllowOrigins,
	}
	if cfg.Config.ExternalControllerCors.AllowPrivateNetwork != nil {
		corsMap["allow-private-network"] = *cfg.Config.ExternalControllerCors.AllowPrivateNetwork
	} else {
		corsMap["allow-private-network"] = true 
	}
	data["external-controller-cors"] = corsMap

	var tunMap map[string]any
	if existingTun, ok := data["tun"].(map[string]any); ok {
		tunMap = existingTun
		if dev, ok := tunMap["device"].(string); ok {
			extracted["tun_device"] = dev
		}
	} else {
		tunMap = make(map[string]any)
		extracted["tun_device"] = ""
	}
	tunMap["enable"] = cfg.Config.Tun.Enable
	data["tun"] = tunMap

	outBytes, err := yaml.Marshal(&data)
	if err != nil {
		return false, nil, fmt.Errorf("运行时配置合成失败: %w", err)
	}

	output := yamlHeader + string(outBytes)
	runtimePath := filepath.Join(baseDir, domain.RuntimeConfigName)

	if existingContent, err := os.ReadFile(runtimePath); err == nil {
		if strings.TrimSpace(string(existingContent)) == strings.TrimSpace(output) {
			slog.Debug("运行时配置无实质变动，跳过磁盘覆写")
			return true, extracted, nil
		}
	}

	if err := writeTmpAndRename(baseDir, runtimePath, []byte(output)); err != nil {
		return false, nil, fmt.Errorf("写入运行时配置失败: %w", err)
	}

	slog.Debug("已成功生成运行时配置", "target", domain.RuntimeConfigName)
	return true, extracted, nil
}

func writeTmpAndRename(baseDir, targetPath string, content []byte) error {
	targetDir := filepath.Dir(targetPath)
	_ = os.MkdirAll(targetDir, 0755)
	tmpFile, err := os.CreateTemp(targetDir, "tmp_*.tmp")
	if err != nil { return err }
	
	tmpName := tmpFile.Name()
	cleaned := false
	defer func() {
		if !cleaned {
			_ = tmpFile.Close()
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmpFile.Write(content); err != nil { return err }
	if err := tmpFile.Sync(); err != nil { return err }
	if err := tmpFile.Close(); err != nil { return err }

	cleaned = true
	return os.Rename(tmpName, targetPath)
}
