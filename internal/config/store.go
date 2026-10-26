package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/fs"
)

type Manager struct {
	baseDir string
	exePath string

	mu   sync.RWMutex
	ioMu sync.Mutex

	data domain.TrayConfig
}

func NewManager(baseDir, exePath string) *Manager {
	return &Manager{
		baseDir: baseDir,
		exePath: exePath,
	}
}

func (m *Manager) LoadAndInitMemory() {
	cfgPath := filepath.Join(m.baseDir, domain.TrayConfigName)
	isTainted := false

	m.mu.Lock()
	if f, err := os.Open(cfgPath); err == nil {
		if err := json.NewDecoder(f).Decode(&m.data); err != nil {
			_ = f.Close()
			slog.Warn("应用配置解析失败，已重置默认", "path", cfgPath, "err", err)

			corruptPath := cfgPath + fmt.Sprintf(".%d.err", time.Now().Unix())
			_ = os.Rename(cfgPath, corruptPath)

			isTainted = true
		} else {
			_ = f.Close()
		}
	} else {
		slog.Debug("应用配置不存在，初始化默认配置", "path", cfgPath)
		isTainted = true
	}

	if applyDefaults(&m.data) || isTainted {
		m.mu.Unlock()
		m.FlushInitialState()
		return
	}
	m.mu.Unlock()
}

func (m *Manager) ReloadFromDisk() error {
	jsonPath := filepath.Join(m.baseDir, domain.TrayConfigName)
	content, err := os.ReadFile(jsonPath)
	if err != nil {
		if os.IsNotExist(err) {
			slog.Warn("应用配置丢失，已从内存恢复")
			m.FlushInitialState()
			return nil
		}
		return err
	}

	var newCfg domain.TrayConfig
	if err := json.Unmarshal(content, &newCfg); err != nil {
		slog.Warn("应用配置损坏，已从内存恢复", "err", err)
		m.FlushInitialState()
		return nil
	}

	isTainted := applyDefaults(&newCfg)

	m.mu.Lock()
	m.data = newCfg
	m.mu.Unlock()

	if isTainted {
		m.FlushInitialState()
	}

	return nil
}

func (m *Manager) GetConfig() domain.TrayConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.data
}

func (m *Manager) Update(updater func(cfg *domain.TrayConfig)) {
	m.ioMu.Lock()
	defer m.ioMu.Unlock()

	m.mu.Lock()
	updater(&m.data)
	snapshotBytes, err := json.MarshalIndent(m.data, "", "  ")
	m.mu.Unlock()

	if err != nil {
		slog.Error("应用配置序列化失败", "err", err)
		return
	}

	cfgPath := filepath.Join(m.baseDir, domain.TrayConfigName)

	if existingBytes, err := os.ReadFile(cfgPath); err == nil && bytes.Equal(existingBytes, snapshotBytes) {
		return
	}

	if err := fs.WriteAtomic(cfgPath, snapshotBytes); err != nil {
		slog.Error("应用配置落盘失败", "err", err)
	}
}

func (m *Manager) FlushInitialState() {
	m.Update(func(cfg *domain.TrayConfig) {})
}

func (m *Manager) GetEffectivePort(p *int, defaultPort int) int {
	if p == nil {
		return defaultPort
	}
	return *p
}

func (m *Manager) GetEffectiveMixedPort() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.GetEffectivePort(m.data.Config.MixedPort, domain.DefaultMixedPort)
}

func (m *Manager) GetEffectiveSystemProxyPort() (int, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.data.Config.MixedPort != nil && *m.data.Config.MixedPort > 0 {
		return *m.data.Config.MixedPort, true
	}
	if m.data.Config.Port != nil && *m.data.Config.Port > 0 {
		return *m.data.Config.Port, true
	}
	return 0, false
}

func (m *Manager) GetEffectiveSystemProxyPortStr() (string, bool) {
	port, ok := m.GetEffectiveSystemProxyPort()
	if !ok {
		return "", false
	}
	return strconv.Itoa(port), true
}

func (m *Manager) GetEffectiveLocalProxyPortStr() string {
	portStr, _ := m.GetEffectiveSystemProxyPortStr()
	return portStr
}

func (m *Manager) GetEffectiveSecret(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (m *Manager) GetEffectiveAllowLan() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.data.Config.AllowLan == nil {
		return domain.DefaultAllowLan
	}
	return *m.data.Config.AllowLan
}

func (m *Manager) GetEffectiveExternalController() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.data.Config.ExternalController == "" {
		return domain.DefaultExternalController
	}
	return m.data.Config.ExternalController
}

func (m *Manager) GetEffectiveRemoteWebUIURL() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.data.General.RemoteWebUIURL != nil && *m.data.General.RemoteWebUIURL != "" {
		return *m.data.General.RemoteWebUIURL
	}
	return domain.DefaultRemoteWebUIURL
}
	
func (m *Manager) IsSystemProxyEnabled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.data.General.SystemProxy != nil && *m.data.General.SystemProxy
}

func (m *Manager) IsAutostartEnabled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.data.General.Autostart != nil && *m.data.General.Autostart
}

func (m *Manager) GetWebOpenMode() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.data.General.WebOpenMode != nil {
		return *m.data.General.WebOpenMode
	}
	return domain.DefaultWebOpenMode
}

func (m *Manager) IsRemoteWebUIEnabled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.data.General.RemoteWebUI != nil && *m.data.General.RemoteWebUI
}

func (m *Manager) BaseDir() string        { return m.baseDir }
func (m *Manager) ExePath() string        { return m.exePath }
func (m *Manager) ProfilesDirAbs() string { return filepath.Join(m.baseDir, domain.ProfilesDir) }

func (m *Manager) GetProfileAbsPath(relPath string) string {
	if relPath == "" {
		return ""
	}
	return filepath.Join(m.baseDir, filepath.FromSlash(relPath))
}
