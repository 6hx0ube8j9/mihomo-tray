package webui

import (
	"errors"
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
			slog.Debug("当前目录受限，缓存已切换至 LocalAppData", "path", fallbackRoot)
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
	if m.IsActive() {
		return errors.New("面板正在运行或启动中，请先关闭")
	}
	if !m.launchMu.TryLock() {
		return errors.New("面板正在运行或启动中，请先关闭")
	}
	defer m.launchMu.Unlock()

	if m.IsActive() {
		return errors.New("面板正在运行或启动中，请先关闭")
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
			slog.Debug("清理面板缓存失败", "path", cacheDir, "err", dirErr)
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
		m.launchPureWebViewMode(cfg, finalURL, eventCh)

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
		slog.Debug("启动系统默认浏览器")
		m.openSystemBrowser(finalURL, eventCh)
		return
	}

	resolvedPath := fs.ResolveBrowserPath(customPath)
	if !fs.IsValidBrowserExecutable(resolvedPath) {
		slog.Error("指定浏览器路径无效或文件不存在", "path", customPath)
		emitEvent(eventCh, EventError)
		return
	}

	slog.Debug("启动指定浏览器打开标签页", "path", resolvedPath)
	m.openCustomBrowserTab(resolvedPath, finalURL, eventCh)
}

func (m *Manager) launchPureWebViewMode(cfg Config, finalURL string, eventCh chan<- Event) {
	cacheRoot, err := m.resolveCacheRoot(cfg.BaseDir)
	if err != nil {
		slog.Error("缓存目录不可写，无法启动内置 WebView2 面板", "err", err)
		emitEvent(eventCh, EventError)
		return
	}

	const sig = domain.WebOpenModeWebView
	m.reconcileSessionLocked(sig)

	if m.tryWakeCachedWindow(eventCh) {
		return
	}

	if m.launchWebView2(cfg, cacheRoot, finalURL, eventCh) {
		m.setActiveSignature(sig)
		slog.Debug("内置面板已启动", "mode", sig)
		return
	}

	slog.Error("内置 WebView2 面板启动失败")
	m.cleanupWebView2()
	emitEvent(eventCh, EventError)
}

func (m *Manager) launchAppMode(cfg Config, customEnabled bool, customPath, finalURL, appHostPort string, eventCh chan<- Event) {
	cacheRoot, err := m.resolveCacheRoot(cfg.BaseDir)
	if err != nil {
		slog.Error("缓存目录不可写，无法启动独立浏览器面板", "err", err)
		emitEvent(eventCh, EventError)
		return
	}

	var browserPath, dirName string
	if customEnabled {
		resolvedPath := fs.ResolveBrowserPath(customPath)
		if !fs.IsValidBrowserExecutable(resolvedPath) {
			slog.Error("指定浏览器路径无效或文件不存在", "path", customPath)
			emitEvent(eventCh, EventError)
			return
		}
		if fs.IsChromiumBrowserExecutable(resolvedPath) {
			browserPath = resolvedPath
			dirName = resolveCustomBrowserDataDir(resolvedPath)
		} else {
			slog.Warn("指定浏览器非 Chromium 内核，App 模式已忽略该配置", "path", customPath)
		}
	}

	if browserPath == "" {
		browserPath, dirName = fs.DetectChromiumBrowser()
		if browserPath == "" {
			slog.Error("未检测到可用的 Chromium 内核浏览器")
			emitEvent(eventCh, EventError)
			return
		}
	}

	sig := buildAppSignature(browserPath, dirName)
	m.reconcileSessionLocked(sig)

	if m.tryWakeCachedWindow(eventCh) {
		return
	}

	if m.launchChromiumAppMode(cfg, cacheRoot, browserPath, dirName, finalURL, appHostPort, eventCh) {
		m.setActiveSignature(sig)
		slog.Debug("独立浏览器面板已启动", "exe", browserPath, "dir", dirName)
		return
	}

	slog.Error("独立浏览器面板启动失败", "exe", browserPath, "dir", dirName)
	m.cleanupChromium()
	emitEvent(eventCh, EventError)
}

func (m *Manager) launchAutoMode(cfg Config, finalURL, appHostPort string, eventCh chan<- Event) {
	cacheRoot, err := m.resolveCacheRoot(cfg.BaseDir)
	if err != nil {
		slog.Warn("缓存目录不可写，使用系统浏览器", "err", err)
		m.openSystemBrowser(finalURL, eventCh)
		return
	}

	const sig = domain.WebOpenModeAuto
	m.reconcileSessionLocked(sig)

	if m.tryWakeCachedWindow(eventCh) {
		return
	}

	if m.launchWebView2(cfg, cacheRoot, finalURL, eventCh) {
		m.setActiveSignature(sig)
		slog.Debug("内置面板已启动", "mode", sig)
		return
	}
	slog.Warn("内置面板启动失败，切换独立浏览器")
	m.cleanupWebView2()

	if browserPath, browserTag := fs.DetectChromiumBrowser(); browserPath != "" {
		if m.launchChromiumAppMode(cfg, cacheRoot, browserPath, browserTag, finalURL, appHostPort, eventCh) {
			m.setActiveSignature(sig)
			slog.Debug("独立浏览器已启动", "browser", browserTag)
			return
		}
	}
	slog.Warn("独立浏览器启动失败，切换系统浏览器")
	m.cleanupChromium()

	m.openSystemBrowser(finalURL, eventCh)
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

func (m *Manager) reconcileSessionLocked(targetSig string) {
	currSig := m.getActiveSignature()
	if currSig != "" && currSig != targetSig {
		slog.Debug("面板启动配置发生变更，重建运行实例", "from", currSig, "to", targetSig)
		m.Cleanup()
	}
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
	if err := sys.ExecuteSystemCommand(`"` + finalURL + `"`); err == nil {
		emitEvent(eventCh, EventReady)
	} else {
		slog.Error("启动系统浏览器失败", "err", err)
		emitEvent(eventCh, EventError)
	}
}

func (m *Manager) openCustomBrowserTab(browserPath, finalURL string, eventCh chan<- Event) {
	cmd := exec.Command(browserPath, finalURL)
	if err := cmd.Start(); err != nil {
		slog.Error("启动指定浏览器失败", "path", browserPath, "err", err)
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
		slog.Debug("窗口失效，正在重置")
		m.Cleanup()
		return false
	}

	sys.BringWindowToFront(hwnd)
	emitEvent(eventCh, EventReady)
	return true
}