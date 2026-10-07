package ui

import (
	"sync"

	"github.com/tailscale/win"
)

type ModalManager struct {
	mu     sync.Mutex
	active map[string]win.HWND
}

func NewModalManager() *ModalManager {
	return &ModalManager{
		active: make(map[string]win.HWND),
	}
}

func (m *ModalManager) TryAcquire(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	if hwnd, exists := m.active[key]; exists {
		ActivateWindow(hwnd)
		return false
	}

	m.active[key] = 0
	return true
}

func (m *ModalManager) RegisterHWND(key string, hwnd win.HWND) {
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
