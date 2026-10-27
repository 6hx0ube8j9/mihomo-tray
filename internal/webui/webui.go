package webui

import (
	"log/slog"
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

func (m *Manager) Launch(cfg Config, eventCh chan<- Event) {
	finalURL, appHostPort := buildFinalURL(cfg)

	mode := cfg.WebOpenMode

	if mode == domain.WebOpenModeSystem {
		slog.Info("根据配置，使用系统默认浏览器进入面板")
		m.openSystemBrowser(finalURL, eventCh)
		return
	}

	if m.tryWakeCachedWindow(eventCh) {
		return
	}

	if !m.launchMu.TryLock() {
		return
	}
	defer m.launchMu.Unlock()

	if mode == domain.WebOpenModeWebView2 {
		if m.launchWebView2(cfg, finalURL, eventCh) {
			slog.Info("已通过 Tier 1 (WebView2 原生容器) 成功启动面板")
			return
		}
		slog.Warn("WebView2 环境不可用或启动失败，尝试降级到独立 Chromium 模式")
	}

	if mode == domain.WebOpenModeWebView2 || mode == domain.WebOpenModeApp {
		if m.launchChromiumAppMode(cfg, finalURL, appHostPort, eventCh) {
			slog.Info("已通过 Tier 2 (独立 Chromium 容器) 成功启动面板")
			return
		}
		slog.Warn("独立 Chromium 运行环境亦不可用，准备执行终极兜底")
	}

	slog.Info("触发 Tier 3 降级：调用系统默认浏览器打开")
	m.openSystemBrowser(finalURL, eventCh)
}

func (m *Manager) Cleanup() {
	m.cleanupWebView2()
	m.cleanupChromium()
}

func (m *Manager) cleanupWebView2() {
	m.wvMu.Lock()
	if m.wv != nil {
		slog.Debug("向 WebView2 窗口发送销毁信号")
		m.wv.Destroy()
		m.wv = nil
	}
	m.wvMu.Unlock()

	m.hwnd.Store(0)
}

func (m *Manager) cleanupChromium() {
	m.stateMu.Lock()
	safeDebugPort := m.debugPort
	m.stateMu.Unlock()

	if safeDebugPort != "" {
		slog.Debug("通过 DevTools 协议发送关闭请求")
		CloseAllWebUITargets(safeDebugPort)
	}

	pid := m.isolatedPid.Load()
	if pid != 0 {
		time.Sleep(500 * time.Millisecond)
		if sys.IsPidRunning(pid, "") {
			slog.Warn("正常关闭超时，强制结束 Chromium 进程", "PID", pid)
			sys.HardKill(pid)
		}
		m.isolatedPid.Store(0)
	}
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
		slog.Error("调用系统默认浏览器失败", "err", err)
		emitEvent(eventCh, EventError)
	}
}

func (m *Manager) tryWakeCachedWindow(eventCh chan<- Event) bool {
	if hwnd := m.hwnd.Load(); hwnd != 0 {
		if sys.IsWindowVisible(hwnd) {
			sys.FocusWindowSilky(hwnd)
			emitEvent(eventCh, EventReady)
			return true
		}
		m.hwnd.Store(0)
	}
	return false
}
