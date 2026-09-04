package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"log/slog"
)

const (
	ConfigFileName = "mihomo-tray.json"

	DefaultAutostart          = "false"
	DefaultProxy              = "false"
	DefaultTun                = "false"
	DefaultMode               = "rule"
	DefaultMixedPort          = "7890"
	DefaultExternalController = "127.0.0.1:9090"
	DefaultSecret             = ""

	DefaultExternalUI    = "ui"
	DefaultExternalUIURL = "https://github.com/Zephyruso/zashboard/releases/latest/download/dist.zip"
	DefaultTunStack      = "mixed"
	DefaultTunAutoRoute  = true
)

type TrayConfig struct {
	Autostart          string `json:"autostart"`
	ExternalController string `json:"external-controller"`
	ExternalUIName     string `json:"external-ui-name"`
	Mode               string `json:"mode"`
	Port               string `json:"port"`
	Proxy              string `json:"proxy"`
	Secret             string `json:"secret"`
	Tun                string `json:"tun"`
	TunDevice          string `json:"tun_device"`	
	TrayLogLevel       string `json:"tray_log_level,omitempty"` 
}

type Manager struct {
	baseDir string
	exePath string
	mu      sync.RWMutex
	yamlMu  sync.Mutex
	data    TrayConfig
}

func NewManager(baseDir, exePath string) *Manager {
	return &Manager{
		baseDir: baseDir,
		exePath: exePath,
	}
}

func (m *Manager) EnsureDefault() {
	m.mu.Lock()
	defer m.mu.Unlock()

	cfgPath := filepath.Join(m.baseDir, ConfigFileName)

	if f, err := os.Open(cfgPath); err == nil {
		if decodeErr := json.NewDecoder(f).Decode(&m.data); decodeErr != nil {
			slog.Error("解析本地配置文件失败，将使用默认值覆盖", "err", decodeErr)
		}
		_ = f.Close()
	} else {
		slog.Info("未找到或无法打开配置文件，将创建全新配置", "Path", cfgPath)
	}

	if m.data.Proxy == "" {
		m.data.Proxy = DefaultProxy
	}
	if m.data.Tun == "" {
		m.data.Tun = DefaultTun
	}
	if m.data.Autostart == "" {
		m.data.Autostart = DefaultAutostart
	}
	if m.data.Mode == "" {
		m.data.Mode = DefaultMode
	}
	if m.data.Port == "" {
		m.data.Port = DefaultMixedPort
	}
	if m.data.ExternalController == "" {
		m.data.ExternalController = DefaultExternalController
	}
	if m.data.Secret == "" {
		m.data.Secret = DefaultSecret
	}

	m.lockedSave()
}

func (m *Manager) Get(key string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	switch key {
	case "autostart":
		return m.data.Autostart
	case "external-controller":
		return m.data.ExternalController
	case "external-ui-name":
		return m.data.ExternalUIName
	case "mode":
		return m.data.Mode
	case "port":
		return m.data.Port
	case "proxy":
		return m.data.Proxy
	case "secret":
		return m.data.Secret
	case "tun":
		return m.data.Tun
	case "tun_device":
		return m.data.TunDevice
	default:
		return ""
	}
}

func (m *Manager) GetJSON(key string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if key == "tray_log_level" {
		return m.data.TrayLogLevel
	}
	return ""
}

func (m *Manager) Set(key, value string) {
	m.UpdateBatch(map[string]string{key: value})
}

func (m *Manager) UpdateBatch(updates map[string]string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	changed := false
	for key, value := range updates {
		if value == "" {
			switch key {
			case "mode":
				value = DefaultMode
			case "port":
				value = DefaultMixedPort
			case "external-controller":
				value = DefaultExternalController
			}
		}

		switch key {
		case "autostart":
			if m.data.Autostart != value {
				m.data.Autostart = value
				changed = true
			}
		case "external-controller":
			if m.data.ExternalController != value {
				m.data.ExternalController = value
				changed = true
			}
		case "external-ui-name":
			if m.data.ExternalUIName != value {
				m.data.ExternalUIName = value
				changed = true
			}
		case "mode":
			if m.data.Mode != value {
				m.data.Mode = value
				changed = true
			}
		case "port":
			if m.data.Port != value {
				m.data.Port = value
				changed = true
			}
		case "proxy":
			if m.data.Proxy != value {
				m.data.Proxy = value
				changed = true
			}
		case "secret":
			if m.data.Secret != value {
				m.data.Secret = value
				changed = true
			}
		case "tun":
			if m.data.Tun != value {
				m.data.Tun = value
				changed = true
			}
		case "tun_device":
			if m.data.TunDevice != value {
				m.data.TunDevice = value
				changed = true
			}
		}
	}

	if changed {
		slog.Debug("配置项变更命中，执行落地存储")
		m.lockedSave()
	}
}

func (m *Manager) PrepareYAMLForBoot() (bool, error) {
	wantMode := m.Get("mode")
	wantTun := m.Get("tun") == "true"

	m.yamlMu.Lock()
	defer m.yamlMu.Unlock()

	configPath := filepath.Join(m.baseDir, "config.yaml")
	content, err := os.ReadFile(configPath)
	if err != nil {
		slog.Error("读取内核 YAML 文件失败", "path", configPath, "err", err)
		return false, err
	}

	rawStr := strings.TrimPrefix(string(content), "\xef\xbb\xbf")
	lines := strings.Split(strings.ReplaceAll(rawStr, "\r\n", "\n"), "\n")

	outLines, extracted, modified := processYAMLContent(lines, wantMode, wantTun)

	if modified {
		slog.Debug("正在下发配置至 config.yaml", "Mode", wantMode, "Tun", wantTun)
		output := strings.Join(outLines, "\n")
		if len(output) > 0 && !strings.HasSuffix(output, "\n") {
			output += "\n"
		}

		if err := writeTmpAndRename(m.baseDir, configPath, []byte(output)); err != nil {
			slog.Error("写入内核 YAML 文件失败", "err", err)
			return false, fmt.Errorf("failed to save config.yaml: %w", err)
		}
	}

	if len(extracted) > 0 {
		m.UpdateBatch(extracted)
	}

	return modified, nil
}

func (m *Manager) lockedSave() {
	b, err := json.MarshalIndent(m.data, "", "  ")
	if err != nil {
		slog.Error("序列化配置文件失败", "err", err)
		return
	}
	cfgPath := filepath.Join(m.baseDir, ConfigFileName)
	_ = writeTmpAndRename(m.baseDir, cfgPath, b)
}

func (m *Manager) BaseDir() string { return m.baseDir }
func (m *Manager) ExePath() string { return m.exePath }
