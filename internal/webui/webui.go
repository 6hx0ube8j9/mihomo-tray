package webui

import (
	"errors"
	"log/slog"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/sys"
)

type webviewSession interface {
	Destroy()
	SetDarkMode(isDark bool)
}

type Event int

const (
	EventReady Event = iota
	EventError
)

type Config struct {
	APIAddr        string
	Secret         string
	ProxyPort      string
	BaseDir        string
	UIName         string
	WebOpenMode    string
	RemoteWebUI    bool
	RemoteWebUIURL string
	IsDark         bool
}

type Manager struct {
	debugPort   string
	isolatedPid atomic.Uint32
	hwnd        atomic.Uintptr
	launchMu    sync.Mutex
	stateMu     sync.Mutex

	wvMu sync.RWMutex
	wv   webviewSession

	envCheckMu  sync.Mutex
	envCheckErr error
	envChecked  bool
}

func NewManager() *Manager {
	return &Manager{}
}

func emitEvent(ch chan<- Event, event Event) {
	if ch != nil {
		select {
		case ch <- event:
		default:
		}
	}
}

func (m *Manager) checkSandboxedEnvironment(baseDir string) error {
	m.envCheckMu.Lock()
	defer m.envCheckMu.Unlock()

	if !m.envChecked {
		m.envCheckErr = sys.ValidateDirWritable(filepath.Join(baseDir, domain.WebCacheDir))
		m.envChecked = true
	}
	return m.envCheckErr
}

func (m *Manager) Launch(cfg Config, eventCh chan<- Event, onReady func()) {
	finalURL, appHostPort := buildFinalURL(cfg)
	mode := cfg.WebOpenMode

	if mode == domain.WebOpenModeSystem {
		slog.Debug("使用系统浏览器打开面板")
		if onReady != nil {
			onReady()
		}
		m.openSystemBrowser(finalURL, eventCh)
		return
	}

	if err := m.checkSandboxedEnvironment(cfg.BaseDir); err != nil {
		slog.Warn("目录无写入权限，改用系统浏览器打开", "err", err)
		if onReady != nil {
			onReady()
		}
		m.openSystemBrowser(finalURL, eventCh)
		return
	}

	if !m.launchMu.TryLock() {
		if onReady != nil {
			onReady()
		}
		return
	}
	defer m.launchMu.Unlock()

	if m.tryWakeCachedWindow(eventCh) {
		if onReady != nil {
			onReady()
		}
		return
	}

	if mode == domain.WebOpenModeWebView2 {
		if m.launchWebView2(cfg, finalURL, eventCh, onReady) {
			slog.Debug("内置面板已启动")
			return
		}
		slog.Warn("内置面板启动失败，尝试独立浏览器")
		m.cleanupWebView2()
	}

	if mode == domain.WebOpenModeWebView2 || mode == domain.WebOpenModeApp {
		if m.launchChromiumAppMode(cfg, finalURL, appHostPort, eventCh, onReady) {
			slog.Debug("独立浏览器已启动")
			return
		}
		slog.Warn("独立浏览器启动失败，尝试系统浏览器")
		m.cleanupChromium()
	}

	if onReady != nil {
		onReady()
	}
	m.openSystemBrowser(finalURL, eventCh)
}

func (m *Manager) Cleanup() {
	m.cleanupWebView2()
	m.cleanupChromium()
}

func (m *Manager) cleanupWebView2() {
	m.wvMu.Lock()
	if m.wv != nil {
		slog.Debug("关闭内置面板")
		m.wv.Destroy()
		m.wv = nil
	}
	m.wvMu.Unlock()

	m.hwnd.Store(0)
}

func (m *Manager) cleanupChromium() {
	m.stateMu.Lock()
	safeDebugPort := m.debugPort
	m.debugPort = ""
	m.stateMu.Unlock()

	if safeDebugPort != "" {
		slog.Debug("关闭独立浏览器面板")
		CloseAllWebUITargets(safeDebugPort)
	}

	pid := m.isolatedPid.Swap(0)
	if pid != 0 {
		time.Sleep(300 * time.Millisecond)
		if sys.IsPidRunning(pid, "") {
			slog.Debug("清理未响应的浏览器进程", "pid", pid)
			sys.HardKill(pid)
		}
	}

	m.hwnd.Store(0)
}

func (m *Manager) UpdateTheme(isDark bool) {
	m.wvMu.RLock()
	defer m.wvMu.RUnlock()

	if m.wv != nil {
		m.wv.SetDarkMode(isDark)
	}
}

func (m *Manager) IsActive() bool {
	m.wvMu.RLock()
	isNativeActive := m.wv != nil
	m.wvMu.RUnlock()

	pid := m.isolatedPid.Load()
	isChromiumActive := false
	if pid != 0 {
		isChromiumActive = sys.IsPidRunning(pid, "")
		if !isChromiumActive {
			m.isolatedPid.Store(0)
			m.stateMu.Lock()
			m.debugPort = ""
			m.stateMu.Unlock()
		}
	}

	return isNativeActive || isChromiumActive
}

func (m *Manager) openSystemBrowser(finalURL string, eventCh chan<- Event) {
	if err := sys.ExecuteSystemCommand(`"` + finalURL + `"`); err == nil {
		emitEvent(eventCh, EventReady)
	} else {
		slog.Error("启动系统浏览器失败", "err", err)
		emitEvent(eventCh, EventError)
	}
}

func (m *Manager) tryWakeCachedWindow(eventCh chan<- Event) bool {
	hwnd := m.hwnd.Load()
	if hwnd == 0 {
		return false
	}

	if !sys.IsWindowAliveAndResponsive(hwnd) {
		slog.Debug("面板窗口已失效，准备重置")
		m.Cleanup()
		return false
	}

	sys.FocusWindowSilky(hwnd)
	emitEvent(eventCh, EventReady)
	return true
}
