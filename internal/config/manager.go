package config

import (
	"encoding/json"
	"fmt"
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
	DefaultSocksPort          = "7891"
	DefaultHTTPPort           = "7892"
	DefaultExternalController = "127.0.0.1:9090"
	DefaultSecret             = ""
	DefaultTunDevice          = "Meta"

	DefaultExternalUI    = "ui"
	DefaultExternalUIURL = ""
	DefaultTunStack      = "mixed"
	DefaultTunAutoRoute  = true
)

type TrayConfig struct {
	Autostart          string `json:"autostart"`
	ExternalController string `json:"external-controller"`
	Mode               string `json:"mode"`
	Port               string `json:"port"`
	Proxy              string `json:"proxy"`
	Secret             string `json:"secret"`
	Tun                string `json:"tun"`
	TunDevice          string `json:"tun_device"`
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
		_ = json.NewDecoder(f).Decode(&m.data)
		_ = f.Close()
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
	if m.data.TunDevice == "" {
		m.data.TunDevice = DefaultTunDevice
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
	case "autostart": return m.data.Autostart
	case "external-controller": return m.data.ExternalController
	case "mode": return m.data.Mode
	case "port": return m.data.Port
	case "proxy": return m.data.Proxy
	case "secret": return m.data.Secret
	case "tun": return m.data.Tun
	case "tun_device": return m.data.TunDevice
	default: return ""
	}
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
			case "tun_device":
				value = DefaultTunDevice
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
		return false, err
	}

	content = bytes.TrimPrefix(content, []byte("\xef\xbb\xbf"))
	
	newContent, extracted, modified, err := processYAMLAST(content, wantMode, wantTun)
	if err != nil {
		return false, fmt.Errorf("解析或修改配置文件失败: %w", err)
	}

	if modified {
		if err := writeTmpAndRename(m.baseDir, configPath, newContent); err != nil {
			return false, fmt.Errorf("保存 config.yaml 失败: %w", err)
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
		return
	}
	cfgPath := filepath.Join(m.baseDir, ConfigFileName)
	_ = writeTmpAndRename(m.baseDir, cfgPath, b)
}

func (m *Manager) BaseDir() string { return m.baseDir }
func (m *Manager) ExePath() string { return m.exePath }
