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


func checkSandboxedEnvironment(baseDir string) error {
	cleanBase := filepath.Clean(baseDir)

	if sys.IsOtherUserProfileDir(cleanBase) {
		return errors.New("受限于其他用户目录隐私隔离")
	}

	if sys.IsAdmin() && sys.IsSystemProtectedDir(cleanBase) {
		return errors.New("受限于系统保护目录，沙盒无权写入")
	}

	cacheBaseDir := filepath.Join(cleanBase, domain.WebCacheDir)
	if err := sys.ProbeDirWritable(cacheBaseDir); err != nil {
		if sys.IsPermissionError(err) {
			return errors.New("无目录写入权限")
		}
		return err
	}

	return nil
}

func (m *Manager) Launch(cfg Config, eventCh chan<- Event, onReady func()) {
	finalURL, appHostPort := buildFinalURL(cfg)
	mode := cfg.WebOpenMode

	if mode == domain.WebOpenModeSystem {
		slog.Debug("使用系统默认浏览器打开面板")
		if onReady != nil {
			onReady()
		}
		m.openSystemBrowser(finalURL, eventCh)
		return
	}

	if err := checkSandboxedEnvironment(cfg.BaseDir); err != nil {
		slog.Warn("因目录权限限制，回退至系统默认浏览器打开", "err", err)
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
			slog.Debug("内置窗口已就绪")
			return
		}
		slog.Warn("内置窗口启动失败，尝试使用独立浏览器")
		m.cleanupWebView2()
	}

	if mode == domain.WebOpenModeWebView2 || mode == domain.WebOpenModeApp {
		if m.launchChromiumAppMode(cfg, finalURL, appHostPort, eventCh, onReady) {
			slog.Debug("独立浏览器窗口已就绪")
			return
		}
		slog.Warn("独立浏览器启动失败，尝试使用系统默认浏览器")
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
		slog.Debug("关闭内置面板窗口")
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
		slog.Debug("发送退出指令至浏览器", "port", safeDebugPort)
		CloseAllWebUITargets(safeDebugPort)
	}

	pid := m.isolatedPid.Swap(0)
	if pid != 0 {
		time.Sleep(300 * time.Millisecond)
		if sys.IsPidRunning(pid, "") {
			slog.Debug("强制清理未响应的浏览器进程", "pid", pid)
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
		slog.Debug("缓存窗口已失效，重置面板状态", "hwnd", hwnd)
		m.Cleanup()
		return false
	}

	sys.FocusWindowSilky(hwnd)
	emitEvent(eventCh, EventReady)
	return true
}
