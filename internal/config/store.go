package config

import (
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
	isAdmin bool

	mu   sync.RWMutex 
	ioMu sync.Mutex 

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
	cfgPath := filepath.Join(m.baseDir, domain.TrayConfigName)
	isTainted := false

	m.mu.Lock()
	if f, err := os.Open(cfgPath); err == nil {
		if err := json.NewDecoder(f).Decode(&m.data); err != nil {
			slog.Warn("主配置文件解析失败，已自动备份并重置为默认设置", "path", cfgPath, "err", err)
			_ = f.Close()

			corruptPath := cfgPath + fmt.Sprintf(".%d.err", time.Now().Unix())
			_ = os.Rename(cfgPath, corruptPath)

			isTainted = true
		} else {
			_ = f.Close()
		}
	} else {
		slog.Debug("主配置文件不存在，正在初始化默认设置", "path", cfgPath)
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
			slog.Warn("主配置文件丢失，已从内存数据自动恢复")
			m.FlushInitialState()
			return nil
		}
		return err
	}

	var newCfg domain.TrayConfig
	if err := json.Unmarshal(content, &newCfg); err != nil {
		m.FlushInitialState()
		return fmt.Errorf("主配置文件格式已损坏，重载请求已拦截，系统恢复为上一次的有效配置。\n\n%w", err)
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
		slog.Error("配置序列化失败", "err", err)
		return
	}

	cfgPath := filepath.Join(m.baseDir, domain.TrayConfigName)
	if err := fs.WriteAtomic(cfgPath, snapshotBytes); err != nil {
		slog.Error("配置原子落盘失败", "err", err)
	}
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

func (m *Manager) GetEffectiveMixedPortStr() string {
	return strconv.Itoa(m.GetEffectiveMixedPort())
}

func (m *Manager) GetEffectiveSecret(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (m *Manager) FlushInitialState() {
	m.Update(func(cfg *domain.TrayConfig) {})
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
