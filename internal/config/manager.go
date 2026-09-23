package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"mihomo-tray/internal/domain"
)

const (
	ConfigFileName = "mihomo-tray.json"
	ProfilesDir    = "profiles"
)

type Manager struct {
	baseDir string
	exePath string
	isAdmin bool
	mu      sync.RWMutex

	data domain.TrayConfig
}

func NewManager(baseDir, exePath string, isAdmin bool) *Manager {
	return &Manager{
		baseDir: baseDir,
		exePath: exePath,
		isAdmin: isAdmin,
	}
}

func (m *Manager) LoadAndInitMemory() {
	m.mu.Lock()
	defer m.mu.Unlock()

	cfgPath := filepath.Join(m.baseDir, ConfigFileName)
	isTainted := false

	if f, err := os.Open(cfgPath); err == nil {
		if err := json.NewDecoder(f).Decode(&m.data); err != nil {
			slog.Error("配置解析失败，启用默认设置", "path", cfgPath, "err", err)
			isTainted = true
		}
		_ = f.Close()
	} else {
		slog.Info("配置文件不存在，初始化默认设置", "path", cfgPath)
		isTainted = true
	}

	if m.data.General.Autostart == nil { t := domain.DefaultAutostart; m.data.General.Autostart = &t; isTainted = true }
	if m.data.General.SystemBrowser == nil { t := domain.DefaultSystemBrowser; m.data.General.SystemBrowser = &t; isTainted = true }
	if m.data.General.SystemProxy == nil { t := domain.DefaultSystemProxy; m.data.General.SystemProxy = &t; isTainted = true }
	if m.data.General.TrayLogLevel == "" { m.data.General.TrayLogLevel = domain.DefaultTrayLogLevel; isTainted = true }

	if m.data.Config.MixedPort == nil { v := domain.DefaultMixedPort; m.data.Config.MixedPort = &v; isTainted = true }
	if m.data.Config.Port == nil { v := domain.DefaultPort; m.data.Config.Port = &v; isTainted = true }
	if m.data.Config.SocksPort == nil { v := domain.DefaultSocksPort; m.data.Config.SocksPort = &v; isTainted = true }

	if m.data.Config.Mode == "" { m.data.Config.Mode = domain.DefaultMode; isTainted = true }
	if m.data.Config.LogLevel == "" { m.data.Config.LogLevel = domain.DefaultLogLevel; isTainted = true }
	if m.data.Config.AllowLan == nil { t := domain.DefaultAllowLan; m.data.Config.AllowLan = &t; isTainted = true }
	if m.data.Config.UnifiedDelay == nil { t := domain.DefaultUnifiedDelay; m.data.Config.UnifiedDelay = &t; isTainted = true }

	if m.data.Config.Secret == "" { m.data.Config.Secret = generateSecureRandomSecret(12); isTainted = true }
	if m.data.Config.ExternalController == "" { m.data.Config.ExternalController = domain.DefaultExternalController; isTainted = true }
	if m.data.Config.ExternalUI == "" { m.data.Config.ExternalUI = domain.DefaultExternalUI; isTainted = true }
	if m.data.Config.ExternalUIURL == "" { m.data.Config.ExternalUIURL = domain.DefaultExternalUIURL; isTainted = true }
	
	m.data.Config.ExternalControllerPipe = domain.IPCNamedPipe

	if m.data.Config.ExternalControllerCors.AllowOrigins == nil {
		m.data.Config.ExternalControllerCors.AllowOrigins = domain.DefaultAllowOrigins
		isTainted = true
	}
	if m.data.Config.ExternalControllerCors.AllowPrivateNetwork == nil {
		t := domain.DefaultAllowPrivateNetwork
		m.data.Config.ExternalControllerCors.AllowPrivateNetwork = &t
		isTainted = true
	}

	var validItems []domain.ProfileItem
	activeFound := false
	for _, item := range m.data.Profiles.Items {
		if filepath.Dir(filepath.ToSlash(item.Path)) != ProfilesDir {
			isTainted = true
			continue
		}
		validItems = append(validItems, item)
		if m.data.Profiles.Active == item.Path { activeFound = true }
	}
	m.data.Profiles.Items = validItems
	if !activeFound && m.data.Profiles.Active != "" {
		m.data.Profiles.Active = ""
		isTainted = true
	}

	if isTainted { m.lockedSave() }
}

func (m *Manager) GetConfig() domain.TrayConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.data
}

func (m *Manager) Update(updater func(cfg *domain.TrayConfig)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	updater(&m.data)
	m.lockedSave()
}

func (m *Manager) GetEffectivePort(p *int, defaultPort int) int {
	if p == nil { return defaultPort }
	return *p
}

func (m *Manager) FlushInitialState() {
	m.mu.RLock()
	defer m.mu.RUnlock()
	m.lockedSave()
}

func (m *Manager) BaseDir() string { return m.baseDir }
func (m *Manager) ExePath() string { return m.exePath }

func (m *Manager) GetActivePathAbs() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.data.Profiles.Active == "" { return "" }
	return filepath.Join(m.baseDir, filepath.FromSlash(m.data.Profiles.Active))
}

func (m *Manager) GetActivePath() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.data.Profiles.Active
}

func (m *Manager) GetProfiles() []domain.ProfileItem {
	m.mu.RLock()
	defer m.mu.RUnlock()
	res := make([]domain.ProfileItem, len(m.data.Profiles.Items))
	copy(res, m.data.Profiles.Items)
	return res
}

func (m *Manager) SetActiveProfile(relPath string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data.Profiles.Active = relPath
	m.lockedSave()
}

func (m *Manager) RemoveProfile(relPath string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if relPath == m.data.Profiles.Active {
		slog.Info("活跃配置已移除，系统进入空转")
		m.data.Profiles.Active = ""
	}
	var newItems []domain.ProfileItem
	for _, item := range m.data.Profiles.Items {
		if item.Path != relPath { newItems = append(newItems, item) }
	}
	m.data.Profiles.Items = newItems
	m.lockedSave()
}

func (m *Manager) MoveProfile(relPath string, offset int) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, p := range m.data.Profiles.Items {
		if p.Path == relPath {
			targetIdx := i + offset
			if targetIdx < 0 || targetIdx >= len(m.data.Profiles.Items) { return false }
			m.data.Profiles.Items[i], m.data.Profiles.Items[targetIdx] = m.data.Profiles.Items[targetIdx], m.data.Profiles.Items[i]
			m.lockedSave()
			return true
		}
	}
	return false
}

func (m *Manager) ValidatePhysicalFile(relPath string) error {
	if relPath == "" { return fmt.Errorf("配置路径为空") }
	absPath := filepath.Join(m.baseDir, filepath.FromSlash(relPath))
	fi, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) { return fmt.Errorf("物理配置文件已丢失") }
		return fmt.Errorf("无法读取配置文件: %w", err)
	}
	if fi.Size() == 0 { return fmt.Errorf("配置文件已损坏 (0字节)") }
	return nil
}

func generateSecureRandomSecret(length int) string {
	byteLen := (length / 2) + 1 
	b := make([]byte, byteLen)
	if _, err := rand.Read(b); err != nil {
		return "SecureSecret"
	}
	encoded := hex.EncodeToString(b)
	if len(encoded) > length {
		encoded = encoded[:length]
	}
	return encoded
}

func (m *Manager) lockedSave() {
	b, err := json.MarshalIndent(m.data, "", "  ")
	if err != nil {
		slog.Error("配置序列化失败", "err", err)
		return
	}
	cfgPath := filepath.Join(m.baseDir, ConfigFileName)
	_ = writeTmpAndRename(m.baseDir, cfgPath, b)
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
