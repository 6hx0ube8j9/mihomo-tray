package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
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

	data        domain.TrayConfig
	lastFlushed []byte
}

func NewManager(baseDir, exePath string) *Manager {
	return &Manager{
		baseDir: baseDir,
		exePath: exePath,
	}
}

func (m *Manager) LoadAndInitMemory() {
	m.ioMu.Lock()
	defer m.ioMu.Unlock()

	cfgPath := filepath.Join(m.baseDir, domain.TrayConfigName)
	var loaded domain.TrayConfig
	isTainted := false

	raw, err := os.ReadFile(cfgPath)
	if err == nil {
		if err := json.Unmarshal(raw, &loaded); err != nil {
			slog.Error("应用配置解析失败，已重置默认", "path", cfgPath, "err", err)

			corruptPath := cfgPath + fmt.Sprintf(".%d.err", time.Now().Unix())
			_ = os.Rename(cfgPath, corruptPath)

			loaded = domain.TrayConfig{}
			isTainted = true
		}
	} else {
		isTainted = true
	}

	if applyDefaults(&loaded) {
		isTainted = true
	}

	m.mu.Lock()
	m.data = loaded
	m.mu.Unlock()

	if isTainted {
		m.lastFlushed = nil
		m.flushIOLocked()
		return
	}
	m.lastFlushed = raw
}

func (m *Manager) ReloadFromDisk() error {
	m.ioMu.Lock()
	defer m.ioMu.Unlock()

	jsonPath := filepath.Join(m.baseDir, domain.TrayConfigName)
	content, err := os.ReadFile(jsonPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			slog.Warn("应用配置丢失，已从内存恢复")
			m.lastFlushed = nil
			m.flushIOLocked()
			return nil
		}
		return err
	}

	var newCfg domain.TrayConfig
	if err := json.Unmarshal(content, &newCfg); err != nil {
		slog.Warn("应用配置损坏，已从内存恢复", "err", err)
		m.lastFlushed = nil
		m.flushIOLocked()
		return nil
	}

	isTainted := applyDefaults(&newCfg)

	m.mu.Lock()
	m.data = newCfg
	m.mu.Unlock()

	if isTainted {
		m.lastFlushed = nil
		m.flushIOLocked()
	} else {
		m.lastFlushed = content
	}

	return nil
}

func (m *Manager) GetConfig() domain.TrayConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.data.Clone()
}

func (m *Manager) TryUpdate(updater func(cfg *domain.TrayConfig) bool) bool {
	m.ioMu.Lock()
	defer m.ioMu.Unlock()

	m.mu.Lock()
	if !updater(&m.data) {
		m.mu.Unlock()
		return false
	}
	snapshotBytes, err := json.MarshalIndent(m.data, "", "  ")
	m.mu.Unlock()

	if err != nil {
		slog.Error("应用配置序列化失败", "err", err)
		return true
	}

	m.writeDiskIOLocked(snapshotBytes)
	return true
}

func (m *Manager) Update(updater func(cfg *domain.TrayConfig)) {
	m.TryUpdate(func(cfg *domain.TrayConfig) bool {
		updater(cfg)
		return true
	})
}

func (m *Manager) flushIOLocked() {
	m.mu.RLock()
	snapshotBytes, err := json.MarshalIndent(m.data, "", "  ")
	m.mu.RUnlock()
	if err != nil {
		slog.Error("应用配置序列化失败", "err", err)
		return
	}
	m.writeDiskIOLocked(snapshotBytes)
}

func (m *Manager) writeDiskIOLocked(snapshotBytes []byte) {
	if m.lastFlushed != nil && bytes.Equal(m.lastFlushed, snapshotBytes) {
		return
	}

	cfgPath := filepath.Join(m.baseDir, domain.TrayConfigName)
	if err := fs.WriteAtomic(cfgPath, snapshotBytes); err != nil {
		slog.Error("应用配置落盘失败", "err", err)
		return
	}
	m.lastFlushed = snapshotBytes
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
