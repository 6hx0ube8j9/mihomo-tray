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

var (
	errCacheBusy           = errors.New("缓存文件被占用，请稍后重试")
	errPanelBusy           = errors.New("面板运行中，请先关闭")
	errWebView2Unavailable = errors.New("未检测到 WebView2 运行时")
)

type webviewSession interface {
	Destroy()
	SetDarkMode(isDark bool)
}

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
	isolatedExeName string
	activeSignature string
	isolatedPID     atomic.Uint32
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
		}
	}

	if fallbackParent != "" {
		_ = os.Remove(fallbackParent)
	}

	if lastErr != nil {
		return errCacheBusy
	}
	return nil
}

func (m *Manager) Launch(cfg Config, onReady func()) error {
	if onReady != nil {
		defer onReady()
	}

	if !m.launchMu.TryLock() {
		return nil
	}
	defer m.launchMu.Unlock()

	finalURL, appHostPort := buildFinalURL(cfg)
	mode := domain.NormalizeWebOpenMode(cfg.WebOpenMode)
	customPath := strings.TrimSpace(cfg.CustomBrowserPath)

	switch mode {
	case domain.WebOpenModeBrowser:
		return m.launchBrowserMode(cfg.CustomBrowserEnabled, customPath, finalURL)
	case domain.WebOpenModeWebView:
		return m.launchWebViewMode(cfg, finalURL)
	case domain.WebOpenModeApp:
		return m.launchAppMode(cfg, cfg.CustomBrowserEnabled, customPath, finalURL, appHostPort)
	default:
		return m.launchAutoMode(cfg, finalURL, appHostPort)
	}
}

func (m *Manager) launchBrowserMode(customEnabled bool, customPath, finalURL string) error {
	if m.IsActive() {
		m.Cleanup()
	}

	if !customEnabled {
		return m.openSystemBrowser(finalURL)
	}

	resolvedPath := fs.ResolveBrowserPath(customPath)
	if !fs.IsBrowserExecutable(resolvedPath) {
		return fmt.Errorf("自定义浏览器路径无效: %s", customPath)
	}

	return m.openCustomBrowserTab(resolvedPath, finalURL)
}

func (m *Manager) launchWebViewMode(cfg Config, finalURL string) error {
	cacheRoot, err := m.resolveCacheRoot(cfg.BaseDir)
	if err != nil {
		return fmt.Errorf("WebView2 缓存目录不可写: %w", err)
	}

	const sig = domain.WebOpenModeWebView
	if m.tryReuseWindow(sig) {
		return nil
	}

	return m.startWebViewWindow(cfg, cacheRoot, finalURL, sig)
}

func (m *Manager) launchAppMode(cfg Config, customEnabled bool, customPath, finalURL, appHostPort string) error {
	cacheRoot, err := m.resolveCacheRoot(cfg.BaseDir)
	if err != nil {
		return fmt.Errorf("App 模式缓存目录不可写: %w", err)
	}

	browserPath, dirName, err := resolveAppBrowser(customEnabled, customPath)
	if err != nil {
		return err
	}

	sig := buildAppSignature(browserPath, dirName)
	if m.tryReuseWindow(sig) {
		return nil
	}

	return m.startChromiumWindow(cfg, cacheRoot, browserPath, dirName, finalURL, appHostPort, sig)
}

func (m *Manager) launchAutoMode(cfg Config, finalURL, appHostPort string) error {
	cacheRoot, err := m.resolveCacheRoot(cfg.BaseDir)
	if err != nil {
		slog.Warn("缓存目录不可写，改用系统浏览器", "err", err)
		return m.openSystemBrowser(finalURL)
	}

	const sig = domain.WebOpenModeAuto
	if m.tryReuseWindow(sig) {
		return nil
	}

	if err := m.startWebViewWindow(cfg, cacheRoot, finalURL, sig); err == nil {
		return nil
	} else if !errors.Is(err, errWebView2Unavailable) {
		slog.Warn("WebView2 启动失败，尝试降级", "err", err)
	}

	if browserPath, dirName := fs.DetectChromiumBrowser(); browserPath != "" {
		if err := m.startChromiumWindow(cfg, cacheRoot, browserPath, dirName, finalURL, appHostPort, sig); err == nil {
			return nil
		} else {
			slog.Warn("App 模式启动失败，改用系统浏览器", "path", browserPath, "err", err)
		}
	}

	return m.openSystemBrowser(finalURL)
}

func resolveAppBrowser(customEnabled bool, customPath string) (browserPath, dirName string, err error) {
	if customEnabled {
		resolvedPath := fs.ResolveBrowserPath(customPath)
		if !fs.IsBrowserExecutable(resolvedPath) {
			return "", "", fmt.Errorf("自定义浏览器路径无效: %s", customPath)
		}
		if fs.IsChromiumBrowser(resolvedPath) {
			return resolvedPath, fs.ResolveBrowserDataDir(resolvedPath), nil
		}
		slog.Warn("自定义浏览器不支持 App 模式，改用自动检测", "path", customPath)
	}

	browserPath, dirName = fs.DetectChromiumBrowser()
	if browserPath == "" {
		return "", "", errors.New("未找到支持 App 模式的浏览器")
	}
	return browserPath, dirName, nil
}

func (m *Manager) startWebViewWindow(cfg Config, cacheRoot, finalURL, sig string) error {
	if err := m.launchWebView2(cfg, cacheRoot, finalURL); err != nil {
		m.cleanupWebView2()
		return err
	}
	m.setActiveSignature(sig)
	return nil
}

func (m *Manager) startChromiumWindow(cfg Config, cacheRoot, browserPath, dirName, finalURL, appHostPort, sig string) error {
	if err := m.launchChromiumApp(cfg, cacheRoot, browserPath, dirName, finalURL, appHostPort); err != nil {
		m.cleanupChromium()
		return err
	}
	m.setActiveSignature(sig)
	return nil
}

func (m *Manager) tryReuseWindow(targetSig string) bool {
	if currSig := m.getActiveSignature(); currSig != "" && currSig != targetSig {
		m.Cleanup()
		return false
	}
	return m.tryWakeCachedWindow()
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

	pid := m.isolatedPID.Load()
	isChromiumActive := false
	if pid != 0 {
		m.stateMu.Lock()
		exeName := m.isolatedExeName
		debugPort := m.debugPort
		m.stateMu.Unlock()

		isChromiumActive = m.isChromiumProcessAlive(pid, exeName, debugPort)
		if !isChromiumActive && m.isolatedPID.CompareAndSwap(pid, 0) {
			m.hwnd.Store(0)
			m.stateMu.Lock()
			if m.debugPort == debugPort {
				m.debugPort = ""
				m.isolatedExeName = ""
			}
			if !isNativeActive {
				m.activeSignature = ""
			}
			m.stateMu.Unlock()
		}
	}

	return isNativeActive || isChromiumActive
}

func (m *Manager) isChromiumProcessAlive(pid uint32, exeName, debugPort string) bool {
	if debugPort != "" {
		if livePID := sys.FindTCPListenerPID(debugPort); livePID != 0 {
			if livePID != pid {
				m.isolatedPID.Store(livePID)
			}
			return true
		}
		if m.hwnd.Load() != 0 {
			return false
		}
	}
	return exeName != "" && sys.IsPidRunning(pid, exeName)
}

func (m *Manager) openSystemBrowser(finalURL string) error {
	if err := sys.ExecuteSystemCommand(`"` + finalURL + `"`); err != nil {
		return fmt.Errorf("启动系统浏览器失败: %w", err)
	}
	return nil
}

func (m *Manager) openCustomBrowserTab(browserPath, finalURL string) error {
	cmd := exec.Command(browserPath, finalURL)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动自定义浏览器失败: %w", err)
	}

	if cmd.Process != nil {
		sys.AllowSetForegroundWindow(uint32(cmd.Process.Pid))
		_ = cmd.Process.Release()
	}
	return nil
}

func (m *Manager) tryWakeCachedWindow() bool {
	hwnd := m.hwnd.Load()
	if hwnd == 0 {
		return false
	}

	if !sys.IsWindowVisible(hwnd) || sys.IsWindowHung(hwnd) {
		m.Cleanup()
		return false
	}

	sys.BringWindowToFront(hwnd)
	return true
}
