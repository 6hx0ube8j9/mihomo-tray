package config

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	Autostart    string `json:"autostart"`
	Mode         string `json:"mode"`
	Proxy        string `json:"proxy"`
	Tun          string `json:"tun"`
	TrayLogLevel string `json:"tray_log_level"`
}

type Manager struct {
	baseDir string
	exePath string
	mu      sync.RWMutex
	yamlMu  sync.Mutex

	data TrayConfig
	runtimeKernelParams map[string]string
}

func NewManager(baseDir, exePath string) *Manager {
	return &Manager{
		baseDir: baseDir,
		exePath: exePath,
		runtimeKernelParams: map[string]string{
			"port":                DefaultMixedPort,
			"external-controller": DefaultExternalController,
			"secret":              DefaultSecret,
		},
	}
}

func (m *Manager) LoadAndInitMemory() {
	m.mu.Lock()
	defer m.mu.Unlock()

	cfgPath := filepath.Join(m.baseDir, ConfigFileName)

	if f, err := os.Open(cfgPath); err == nil {
		if decodeErr := json.NewDecoder(f).Decode(&m.data); decodeErr != nil {
			slog.Error("解析本地配置文件失败，内存将使用空状态", "err", decodeErr)
		}
		_ = f.Close()
	} else {
		slog.Info("未找到配置文件，内存将作为全新配置初始化", "Path", cfgPath)
	}

	if m.data.Proxy == "" {
		m.data.Proxy = DefaultProxy
	}
	if m.data.Tun == "" {
		m.data.Tun = DefaultTun
	}
	if m.data.Mode == "" {
		m.data.Mode = DefaultMode
	}
	if m.data.TrayLogLevel == "" {
		m.data.TrayLogLevel = "error"
	}
}

func (m *Manager) FlushInitialState() {
	m.mu.RLock()
	defer m.mu.RUnlock()
	m.lockedSave()
}

func (m *Manager) Get(key string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	switch key {
	case "autostart":
		return m.data.Autostart
	case "mode":
		return m.data.Mode
	case "proxy":
		return m.data.Proxy
	case "tun":
		return m.data.Tun
	default:
		return m.runtimeKernelParams[key]
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

	diskChanged := false
	for key, value := range updates {
		switch key {
		case "autostart":
			if m.data.Autostart != value {
				m.data.Autostart = value
				diskChanged = true
			}
		case "mode":
			if m.data.Mode != value {
				m.data.Mode = value
				diskChanged = true
			}
		case "proxy":
			if m.data.Proxy != value {
				m.data.Proxy = value
				diskChanged = true
			}
		case "tun":
			if m.data.Tun != value {
				m.data.Tun = value
				diskChanged = true
			}
		default:
			m.runtimeKernelParams[key] = value
		}
	}

	if diskChanged {
		slog.Debug("本地用户偏好发生变更，保存至 JSON")
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
		slog.Debug("正在更新 config.yaml 参数", "Mode", wantMode, "Tun", wantTun)
		output := strings.Join(outLines, "\n")
		if len(output) > 0 && !strings.HasSuffix(output, "\n") {
			output += "\n"
		}

		if err := writeTmpAndRename(m.baseDir, configPath, []byte(output)); err != nil {
			slog.Error("保存内核 YAML 文件失败", "err", err)
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
