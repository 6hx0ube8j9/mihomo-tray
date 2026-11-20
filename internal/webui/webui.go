package webui

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/fs"
	"mihomo-tray/internal/sys"
)

var errPanelBusy = errors.New("面板正在运行或启动中，请先关闭")

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
	APIAddr              string
	Secret               string
	ProxyPort            string
	BaseDir              string
	UIName               string
	WebOpenMode          string
	RemoteWebUI          bool
	RemoteWebUIURL       string
	CustomBrowserEnabled bool
	CustomBrowserPath    string
	IsDark               bool
}

type Manager struct {
	debugPort       string
	activeSignature string
	isolatedPid     atomic.Uint32
	hwnd            atomic.Uintptr
	launchMu        sync.Mutex
	stateMu         sync.Mutex

	wvMu sync.RWMutex
	wv   webviewSession

	cacheDirMu       sync.Mutex
	resolvedCacheDir string
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

func buildProxyArgs(proxyPort string) []string {
	p := strings.TrimSpace(proxyPort)
	if p == "" || p == "0" {
		return nil
	}
	return []string{
		"--proxy-server=" + net.JoinHostPort(domain.LocalhostIP, p),
		"--proxy-bypass-list=127.0.0.1;localhost;<local>",
	}
}

func buildAppSignature(browserPath, dataDir string) string {
	cleanPath := strings.ToLower(filepath.Clean(strings.TrimSpace(browserPath)))
	cleanDir := strings.ToLower(strings.TrimSpace(dataDir))
	return fmt.Sprintf("%s|%s|%s", domain.WebOpenModeApp, cleanPath, cleanDir)
}

func (m *Manager) resolveCacheRoot(baseDir string) (string, error) {
	m.cacheDirMu.Lock()
	defer m.cacheDirMu.Unlock()

	if m.resolvedCacheDir != "" {
		return m.resolvedCacheDir, nil
	}

	var primaryErr error
	if baseDir != "" {
		primaryRoot := filepath.Join(baseDir, domain.WebCacheDir)
		if err := sys.ValidateDirWritable(primaryRoot); err == nil {
			m.resolvedCacheDir = primaryRoot
			return primaryRoot, nil
		} else {
			primaryErr = err
		}
	}

	if userCache, err := os.UserCacheDir(); err == nil && userCache != "" {
		fallbackRoot := filepath.Join(userCache, domain.FallbackCacheAppDir, domain.WebCacheDir)
		if err := sys.ValidateDirWritable(fallbackRoot); err == nil {
			slog.Debug("缓存切换至用户目录", "path", fallbackRoot)
			m.resolvedCacheDir = fallbackRoot
			return fallbackRoot, nil
		} else if primaryErr == nil {
			primaryErr = err
		}
	}

	if primaryErr != nil {
		return "", primaryErr
	}
	return "", sys.ErrInvalidPath
}

func (m *Manager) ClearCache(baseDir string) error {
	if !m.launchMu.TryLock() {
		return errPanelBusy
	}
	defer m.launchMu.Unlock()

	if m.IsActive() {
		return errPanelBusy
	}

	m.cacheDirMu.Lock()
	m.resolvedCacheDir = ""
	m.cacheDirMu.Unlock()

	var targets []string
	if baseDir != "" {
		targets = append(targets, filepath.Clean(filepath.Join(baseDir, domain.WebCacheDir)))
	}

	var fallbackParent string
	if userCache, err := os.UserCacheDir(); err == nil && userCache != "" {
		fallbackParent = filepath.Clean(filepath.Join(userCache, domain.FallbackCacheAppDir))
		fallbackCache := filepath.Join(fallbackParent, domain.WebCacheDir)
		if len(targets) == 0 || !strings.EqualFold(targets[0], fallbackCache) {
			targets = append(targets, fallbackCache)
		}
	}

	var lastErr error
	for _, cacheDir := range targets {
		var dirErr error
		for i := 0; i < 2; i++ {
			if err := os.RemoveAll(cacheDir); err == nil {
				dirErr = nil
				break
			} else {
				dirErr = err
				if i == 0 {
					time.Sleep(120 * time.Millisecond)
				}
			}
		}
		if dirErr != nil {
			lastErr = dirErr
			slog.Warn("清理面板缓存失败", "path", cacheDir, "err", dirErr)
		}
	}

	if fallbackParent != "" {
		_ = os.Remove(fallbackParent)
	}

	if lastErr != nil {
		return errors.New("缓存文件被占用，请稍后重试")
	}
	return nil
}

func (m *Manager) Launch(cfg Config, eventCh chan<- Event, onReady func()) {
	if onReady != nil {
		defer onReady()
	}

	if !m.launchMu.TryLock() {
		return
	}
	defer m.launchMu.Unlock()

	finalURL, appHostPort := buildFinalURL(cfg)
	mode := domain.NormalizeWebOpenMode(cfg.WebOpenMode)
	customPath := strings.TrimSpace(cfg.CustomBrowserPath)

	switch mode {
	case domain.WebOpenModeBrowser:
		m.launchBrowserMode(cfg.CustomBrowserEnabled, customPath, finalURL, eventCh)
	case domain.WebOpenModeWebView:
		m.launchWebViewMode(cfg, finalURL, eventCh)
	case domain.WebOpenModeApp:
		m.launchAppMode(cfg, cfg.CustomBrowserEnabled, customPath, finalURL, appHostPort, eventCh)
	default:
		m.launchAutoMode(cfg, finalURL, appHostPort, eventCh)
	}
}

func (m *Manager) launchBrowserMode(customEnabled bool, customPath, finalURL string, eventCh chan<- Event) {
	if m.IsActive() {
		m.Cleanup()
	}

	if !customEnabled {
		m.openSystemBrowser(finalURL, eventCh)
		return
	}

	resolvedPath := fs.ResolveBrowserPath(customPath)
	if !fs.IsBrowserExecutable(resolvedPath) {
		slog.Error("自定义浏览器路径无效", "path", customPath)
		emitEvent(eventCh, EventError)
		return
	}

	m.openCustomBrowserTab(resolvedPath, finalURL, eventCh)
}

func (m *Manager) launchWebViewMode(cfg Config, finalURL string, eventCh chan<- Event) {
	cacheRoot, err := m.resolveCacheRoot(cfg.BaseDir)
	if err != nil {
		slog.Error("WebView2 缓存目录不可写", "err", err)
		emitEvent(eventCh, EventError)
		return
	}

	const sig = domain.WebOpenModeWebView
	if m.tryReuseWindow(sig, eventCh) {
		return
	}

	if m.startWebViewWindow(cfg, cacheRoot, finalURL, sig, eventCh) {
		return
	}

	slog.Error("WebView2 启动失败")
	emitEvent(eventCh, EventError)
}

func (m *Manager) launchAppMode(cfg Config, customEnabled bool, customPath, finalURL, appHostPort string, eventCh chan<- Event) {
	cacheRoot, err := m.resolveCacheRoot(cfg.BaseDir)
	if err != nil {
		slog.Error("App 应用模式缓存目录不可写", "err", err)
		emitEvent(eventCh, EventError)
		return
	}

	browserPath, dirName, ok := resolveAppBrowser(customEnabled, customPath)
	if !ok {
		emitEvent(eventCh, EventError)
		return
	}

	sig := buildAppSignature(browserPath, dirName)
	if m.tryReuseWindow(sig, eventCh) {
		return
	}

	if m.startChromiumWindow(cfg, cacheRoot, browserPath, dirName, finalURL, appHostPort, sig, eventCh) {
		return
	}

	slog.Error("App 应用模式启动失败", "path", browserPath)
	emitEvent(eventCh, EventError)
}

func (m *Manager) launchAutoMode(cfg Config, finalURL, appHostPort string, eventCh chan<- Event) {
	cacheRoot, err := m.resolveCacheRoot(cfg.BaseDir)
	if err != nil {
		slog.Warn("缓存目录不可写，改用系统浏览器", "err", err)
		m.openSystemBrowser(finalURL, eventCh)
		return
	}

	const sig = domain.WebOpenModeAuto
	if m.tryReuseWindow(sig, eventCh) {
		return
	}

	if m.startWebViewWindow(cfg, cacheRoot, finalURL, sig, eventCh) {
		return
	}

	if browserPath, dirName := fs.DetectChromiumBrowser(); browserPath != "" {
		slog.Debug("WebView2 不可用，改用 App 应用模式", "browser", dirName)
		if m.startChromiumWindow(cfg, cacheRoot, browserPath, dirName, finalURL, appHostPort, sig, eventCh) {
			return
		}
	}

	m.openSystemBrowser(finalURL, eventCh)
}

func resolveAppBrowser(customEnabled bool, customPath string) (browserPath, dirName string, ok bool) {
	if customEnabled {
		resolvedPath := fs.ResolveBrowserPath(customPath)
		if !fs.IsBrowserExecutable(resolvedPath) {
			slog.Error("自定义浏览器路径无效", "path", customPath)
			return "", "", false
		}
		if fs.IsChromiumBrowser(resolvedPath) {
			return resolvedPath, fs.ResolveBrowserDataDir(resolvedPath), true
		}
		slog.Warn("自定义浏览器不支持 App 应用模式，改用自动检测", "path", customPath)
	}

	browserPath, dirName = fs.DetectChromiumBrowser()
	if browserPath == "" {
		slog.Error("未找到支持 App 应用模式的浏览器")
		return "", "", false
	}
	return browserPath, dirName, true
}

func (m *Manager) startWebViewWindow(cfg Config, cacheRoot, finalURL, sig string, eventCh chan<- Event) bool {
	if m.launchWebView2(cfg, cacheRoot, finalURL, eventCh) {
		m.setActiveSignature(sig)
		slog.Debug("WebView2 已启动", "mode", sig)
		return true
	}
	m.cleanupWebView2()
	return false
}

func (m *Manager) startChromiumWindow(cfg Config, cacheRoot, browserPath, dirName, finalURL, appHostPort, sig string, eventCh chan<- Event) bool {
	if m.launchChromiumApp(cfg, cacheRoot, browserPath, dirName, finalURL, appHostPort, eventCh) {
		m.setActiveSignature(sig)
		slog.Debug("App 应用模式已启动", "browser", dirName)
		return true
	}
	m.cleanupChromium()
	return false
}

func (m *Manager) tryReuseWindow(targetSig string, eventCh chan<- Event) bool {
	if currSig := m.getActiveSignature(); currSig != "" && currSig != targetSig {
		slog.Debug("面板配置变更，重启窗口", "from", currSig, "to", targetSig)
		m.Cleanup()
		return false
	}
	return m.tryWakeCachedWindow(eventCh)
}

func (m *Manager) getActiveSignature() string {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	return m.activeSignature
}

func (m *Manager) setActiveSignature(sig string) {
	m.stateMu.Lock()
	m.activeSignature = sig
	m.stateMu.Unlock()
}

func (m *Manager) Cleanup() {
	m.cleanupWebView2()
	m.cleanupChromium()
	m.setActiveSignature("")
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
			m.hwnd.Store(0)
			m.stateMu.Lock()
			m.debugPort = ""
			if !isNativeActive {
				m.activeSignature = ""
			}
			m.stateMu.Unlock()
		}
	}

	return isNativeActive || isChromiumActive
}

func (m *Manager) openSystemBrowser(finalURL string, eventCh chan<- Event) {
	slog.Debug("启动系统浏览器")
	if err := sys.ExecuteSystemCommand(`"` + finalURL + `"`); err == nil {
		emitEvent(eventCh, EventReady)
	} else {
		slog.Error("启动系统浏览器失败", "err", err)
		emitEvent(eventCh, EventError)
	}
}

func (m *Manager) openCustomBrowserTab(browserPath, finalURL string, eventCh chan<- Event) {
	slog.Debug("启动自定义浏览器", "path", browserPath)
	cmd := exec.Command(browserPath, finalURL)
	if err := cmd.Start(); err != nil {
		slog.Error("启动自定义浏览器失败", "path", browserPath, "err", err)
		emitEvent(eventCh, EventError)
		return
	}

	if cmd.Process != nil {
		sys.AllowSetForegroundWindow(uint32(cmd.Process.Pid))
		_ = cmd.Process.Release()
	}
	emitEvent(eventCh, EventReady)
}

func (m *Manager) tryWakeCachedWindow(eventCh chan<- Event) bool {
	hwnd := m.hwnd.Load()
	if hwnd == 0 {
		return false
	}

	if !sys.IsWindowVisible(hwnd) || sys.IsWindowHung(hwnd) {
		slog.Debug("面板窗口失效，重新打开")
		m.Cleanup()
		return false
	}

	sys.BringWindowToFront(hwnd)
	emitEvent(eventCh, EventReady)
	return true
}