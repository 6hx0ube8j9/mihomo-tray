package ui

import (
	"log/slog"
	"sync"

	"mihomo-tray/internal/sys"
)

type ModalManager struct {
	mu     sync.Mutex
	active map[string]uintptr
}

func NewModalManager() *ModalManager {
	return &ModalManager{
		active: make(map[string]uintptr),
	}
}

func (m *ModalManager) TryAcquire(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	if hwnd, exists := m.active[key]; exists {
		if hwnd != 0 {
			slog.Debug("弹窗已存在，激活已有窗口", "key", key, "hwnd", hwnd)
			sys.ActivateWindow(hwnd)
		}
		return false
	}

	m.active[key] = 0
	return true
}

func (m *ModalManager) RegisterHWND(key string, hwnd uintptr) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.active[key]; exists {
		m.active[key] = hwnd
	}
}

func (m *ModalManager) Release(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.active, key)
}