package webui

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"mihomo-tray/internal/sys"
)

type Event int

const (
	EventReady Event = iota
	EventError
)

type Config struct {
	APIAddr            string
	Secret             string
	ProxyPort          string
	BaseDir            string
	UIName             string
	ForceSystemBrowser bool
	RemoteWebUI        bool
	RemoteWebUIURL     string
	SystemTheme        string
}

// Manager 负责 WebUI 的生命周期调度
type Manager struct {
	debugPort   string
	isolatedPid atomic.Uint32
	hwnd        atomic.Uintptr
	launchMu    sync.Mutex
	stateMu     sync.Mutex
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

// Launch 执行多级降级启动策略
func (m *Manager) Launch(cfg Config, eventCh chan<- Event) {
	finalURL, appHostPort := buildFinalURL(cfg)

	// 0. 用户强制要求使用系统默认浏览器
	if cfg.ForceSystemBrowser {
		slog.Info("根据全局配置，强制使用系统默认浏览器进入面板")
		m.openSystemBrowser(finalURL, eventCh)
		return
	}

	// 1. 尝试唤醒已存在（但可能被最小化）的窗口
	if m.tryWakeCachedWindow(eventCh) {
		return
	}

	// 防并发锁
	if !m.launchMu.TryLock() {
		return
	}
	defer m.launchMu.Unlock()

	// 2. Tier 1: 尝试启动原生 WebView2
	if m.launchWebView2(cfg, finalURL, eventCh) {
		slog.Info("已通过 Tier 1 (WebView2 原生容器) 成功启动面板")
		return
	}

	// 3. Tier 2: 降级到独立 Chromium 进程 (--app 模式)
	if m.launchChromiumAppMode(cfg, finalURL, appHostPort, eventCh) {
		return
	}

	// 4. Tier 3: 终极兜底，直接调用系统默认浏览器
	slog.Warn("WebView2 与独立 Chromium 环境均不可用，降级为默认浏览器打开")
	m.openSystemBrowser(finalURL, eventCh)
}

func (m *Manager) Cleanup() {
	m.hwnd.Store(0)

	m.stateMu.Lock()
	safeDebugPort := m.debugPort
	m.stateMu.Unlock()

	if safeDebugPort != "" {
		slog.Debug("通过 DevTools 协议发送关闭请求")
		CloseAllWebUITargets(safeDebugPort)
	}

	time.Sleep(500 * time.Millisecond)
	pid := m.isolatedPid.Load()
	if pid != 0 && sys.IsPidRunning(pid, "") {
		slog.Warn("正常关闭超时，强制结束浏览器进程", "PID", pid)
		sys.HardKill(pid)
	}
	m.isolatedPid.Store(0)
}

func (m *Manager) IsActive() bool {
	hwnd := m.hwnd.Load()
	return hwnd != 0 && sys.IsWindowVisible(hwnd)
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
