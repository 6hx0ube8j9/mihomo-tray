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
	ProfilesDir    = "profiles"

	DefaultAutostart          = "false"
	DefaultProxy              = "false"
	DefaultTun                = "false"
	DefaultMode               = "rule"
	DefaultMixedPort          = "7890"
	DefaultExternalController = "127.0.0.1:9090"
	DefaultSecret             = ""

	DefaultExternalUI    = "ui"
	DefaultExternalUIURL = "https://github.com/Zephyruso/zashboard/releases/latest/download/dist.zip"
)

type TrayConfig struct {
	Autostart    string        `json:"autostart"`
	RunAsAdmin   string        `json:"run_as_admin"`
	Mode         string        `json:"mode"`
	Proxy        string        `json:"proxy"`
	Tun          string        `json:"tun"`
	TrayLogLevel string        `json:"tray_log_level"`
	Active       string        `json:"active"`
	Items        []ProfileItem `json:"items"`
}

type Manager struct {
	baseDir string
	exePath string
	isAdmin bool
	mu      sync.RWMutex
	yamlMu  sync.Mutex

	data                TrayConfig
	runtimeKernelParams map[string]string
}

func NewManager(baseDir, exePath string, isAdmin bool) *Manager {
	return &Manager{
		baseDir: baseDir,
		exePath: exePath,
		isAdmin: isAdmin,
		runtimeKernelParams: map[string]string{
			"port": DefaultMixedPort,
		},
	}
}

func (m *Manager) ensureDefaultProfileExists() {
	profilesDirAbs := filepath.Join(m.baseDir, ProfilesDir)
	_ = os.MkdirAll(profilesDirAbs, 0755)

	defaultPath := filepath.Join(profilesDirAbs, "default.yaml")
	if _, err := os.Stat(defaultPath); os.IsNotExist(err) {
		content := fmt.Sprintf("mixed-port: %s\nmode: %s\nexternal-controller: %s\nsecret: '%s'\nexternal-ui: '%s'\nexternal-ui-url: '%s'\ntun:\n  enable: false\n",
			DefaultMixedPort, DefaultMode, DefaultExternalController, DefaultSecret, DefaultExternalUI, DefaultExternalUIURL)
		
		if err := os.WriteFile(defaultPath, []byte(content), 0644); err == nil {
			slog.Info("已自动生成保底示例配置", "path", defaultPath)
		}
	}
}

func (m *Manager) LoadAndInitMemory() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.ensureDefaultProfileExists()

	cfgPath := filepath.Join(m.baseDir, ConfigFileName)
	isTainted := false

	if f, err := os.Open(cfgPath); err == nil {
		if decodeErr := json.NewDecoder(f).Decode(&m.data); decodeErr != nil {
			slog.Error("解析配置文件失败，触发坏文件降级策略", "path", cfgPath, "err", decodeErr)
			isTainted = true
		}
		_ = f.Close()
	} else {
		slog.Info("未找到配置文件，初始化默认配置状态", "path", cfgPath)
		isTainted = true
	}

	var validItems []ProfileItem
	for _, item := range m.data.Items {
		rel := filepath.ToSlash(item.Path)
		if !strings.HasPrefix(rel, ProfilesDir+"/") || strings.Contains(rel, "..") || filepath.IsAbs(rel) {
			slog.Warn("拦截并清理不合规的遗留配置", "path", item.Path)
			isTainted = true
			continue
		}
		validItems = append(validItems, item)
	}
	m.data.Items = validItems

	if len(m.data.Items) == 0 {
		m.data.Items = []ProfileItem{{Name: "default", Path: filepath.ToSlash(filepath.Join(ProfilesDir, "default.yaml"))}}
		isTainted = true
	}

	activeFound := false
	for _, item := range m.data.Items {
		if m.data.Active == item.Path {
			activeFound = true
			break
		}
	}
	if !activeFound {
		m.data.Active = m.data.Items[0].Path
		isTainted = true
	}

	activeAbs := filepath.Join(m.baseDir, filepath.FromSlash(m.data.Active))
	if _, err := os.Stat(activeAbs); err != nil {
		slog.Warn("当前活跃配置物理文件已丢失，执行安全回退", "missing", m.data.Active)
		m.data.Active = m.data.Items[0].Path
		isTainted = true
	}

	if m.data.RunAsAdmin == "" { m.data.RunAsAdmin = "false"; isTainted = true }
	if m.data.Proxy == "" { m.data.Proxy = DefaultProxy; isTainted = true }
	if m.data.Tun == "" { m.data.Tun = DefaultTun; isTainted = true }
	if m.data.Mode == "" { m.data.Mode = DefaultMode; isTainted = true }
	if m.data.TrayLogLevel == "" { m.data.TrayLogLevel = "error"; isTainted = true }

	if isTainted {
		m.lockedSave()
	}
}

func (m *Manager) FlushInitialState() {
	m.mu.RLock()
	defer m.mu.RUnlock()
	m.lockedSave()
}

func (m *Manager) GetActivePathAbs() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return filepath.Join(m.baseDir, filepath.FromSlash(m.data.Active))
}

func (m *Manager) GetActivePath() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.data.Active
}

func (m *Manager) GetProfiles() []ProfileItem {
	m.mu.RLock()
	defer m.mu.RUnlock()
	res := make([]ProfileItem, len(m.data.Items))
	copy(res, m.data.Items)
	return res
}

func (m *Manager) SetActiveProfile(relPath string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data.Active = relPath
	m.lockedSave()
}

func (m *Manager) RemoveProfile(relPath string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	if relPath == m.data.Active {
		slog.Warn("拦截删除请求：无法删除正在使用的活跃配置")
		return
	}
	if len(m.data.Items) <= 1 {
		slog.Warn("拦截删除请求：必须至少保留一个配置")
		return
	}

	var newItems []ProfileItem
	for _, item := range m.data.Items {
		if item.Path != relPath {
			newItems = append(newItems, item)
		}
	}
	m.data.Items = newItems
	m.lockedSave()
}

func (m *Manager) Get(key string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	switch key {
	case "autostart": return m.data.Autostart
	case "run_as_admin": return m.data.RunAsAdmin
	case "mode": return m.data.Mode
	case "proxy": return m.data.Proxy
	case "tun": return m.data.Tun
	default: return m.runtimeKernelParams[key]
	}
}

func (m *Manager) GetJSON(key string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if key == "tray_log_level" { return m.data.TrayLogLevel }
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
			if m.data.Autostart != value { m.data.Autostart = value; diskChanged = true }
		case "run_as_admin":
			if m.data.RunAsAdmin != value { m.data.RunAsAdmin = value; diskChanged = true }
		case "mode":
			if m.data.Mode != value { m.data.Mode = value; diskChanged = true }
		case "proxy":
			if m.data.Proxy != value { m.data.Proxy = value; diskChanged = true }
		case "tun":
			if m.data.Tun != value { m.data.Tun = value; diskChanged = true }
		default:
			m.runtimeKernelParams[key] = value
		}
	}

	if diskChanged {
		m.lockedSave()
	}
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
