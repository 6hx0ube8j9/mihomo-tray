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

	"mihomo-tray/internal/browser"
	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/sys"
)

var (
	ErrCacheBusy           = errors.New("缓存文件被占用，请稍后重试")
	ErrPanelBusy           = errors.New("面板运行中，请先关闭")
	ErrWebView2Unavailable = errors.New("未安装 WebView2 运行时")
	ErrInvalidBrowserPath  = errors.New("自定义浏览器路径无效")
	ErrBrowserNotFound     = errors.New("未找到支持 App 模式的浏览器")

	errLaunchAborted = errors.New("面板启动已中止")
)

type webviewSession interface {
	Destroy()
	SetDarkMode(isDark bool)
	Reload() error
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
	cleanupGen      atomic.Uint64
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

func (m *Manager) isLaunchStale(launchGen uint64) bool {
	return m.cleanupGen.Load() != launchGen
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
		return ErrPanelBusy
	}
	defer m.launchMu.Unlock()

	if m.IsActive() {
		return ErrPanelBusy
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
		return fmt.Errorf("%w: %w", ErrCacheBusy, lastErr)
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

	launchGen := m.cleanupGen.Load()
	finalURL, appHostPort := buildFinalURL(cfg)
	mode := domain.NormalizeWebOpenMode(cfg.WebOpenMode)
	customPath := strings.TrimSpace(cfg.CustomBrowserPath)

	switch mode {
	case domain.WebOpenModeBrowser:
		return m.launchBrowserMode(cfg.BaseDir, cfg.CustomBrowserEnabled, customPath, finalURL, launchGen)
	case domain.WebOpenModeWebView:
		return m.launchWebViewMode(cfg, finalURL, launchGen)
	case domain.WebOpenModeApp:
		return m.launchAppMode(cfg, cfg.CustomBrowserEnabled, customPath, finalURL, appHostPort, launchGen)
	default:
		return m.launchAutoMode(cfg, finalURL, appHostPort, launchGen)
	}
}

func (m *Manager) launchBrowserMode(baseDir string, customEnabled bool, customPath, finalURL string, launchGen uint64) error {
	if m.IsActive() {
		m.cleanupSessions()
	}
	if m.isLaunchStale(launchGen) {
		return nil
	}

	if !customEnabled {
		return m.openSystemBrowser(finalURL)
	}

	resolvedPath := browser.ResolvePath(customPath, baseDir)
	if !browser.IsExecutable(resolvedPath, baseDir) {
		return fmt.Errorf("%w: %s", ErrInvalidBrowserPath, customPath)
	}

	return m.openCustomBrowserTab(resolvedPath, finalURL)
}

func (m *Manager) launchWebViewMode(cfg Config, finalURL string, launchGen uint64) error {
	cacheRoot, err := m.resolveCacheRoot(cfg.BaseDir)
	if err != nil {
		return fmt.Errorf("WebView2 缓存目录不可写: %w", err)
	}

	const sig = domain.WebOpenModeWebView
	if m.tryReuseWindow(sig) {
		return nil
	}

	return m.startWebViewWindow(cfg, cacheRoot, finalURL, sig, launchGen)
}

func (m *Manager) launchAppMode(cfg Config, customEnabled bool, customPath, finalURL, appHostPort string, launchGen uint64) error {
	cacheRoot, err := m.resolveCacheRoot(cfg.BaseDir)
	if err != nil {
		return fmt.Errorf("App 模式缓存目录不可写: %w", err)
	}

	browserPath, dirName, err := resolveAppBrowser(cfg.BaseDir, customEnabled, customPath)
	if err != nil {
		return err
	}

	sig := buildAppSignature(browserPath, dirName)
	if m.tryReuseWindow(sig) {
		return nil
	}

	return m.startChromiumWindow(cfg, cacheRoot, browserPath, dirName, finalURL, appHostPort, sig, launchGen)
}

func (m *Manager) launchAutoMode(cfg Config, finalURL, appHostPort string, launchGen uint64) error {
	cacheRoot, err := m.resolveCacheRoot(cfg.BaseDir)
	if err != nil {
		if m.isLaunchStale(launchGen) {
			return nil
		}
		slog.Warn("缓存目录不可写，改用系统浏览器", "err", err)
		return m.openSystemBrowser(finalURL)
	}

	const sig = domain.WebOpenModeAuto
	if m.tryReuseWindow(sig) {
		return nil
	}

	if err := m.startWebViewWindow(cfg, cacheRoot, finalURL, sig, launchGen); err == nil {
		return nil
	} else if !errors.Is(err, ErrWebView2Unavailable) {
		slog.Warn("WebView2 启动失败，尝试降级", "err", err)
	}

	if m.isLaunchStale(launchGen) {
		return nil
	}

	if browserPath, dirName := browser.DetectChromium(); browserPath != "" {
		if err := m.startChromiumWindow(cfg, cacheRoot, browserPath, dirName, finalURL, appHostPort, sig, launchGen); err == nil {
			return nil
		} else {
			slog.Warn("App 模式启动失败，改用系统浏览器", "path", browserPath, "err", err)
		}
	}

	if m.isLaunchStale(launchGen) {
		return nil
	}

	return m.openSystemBrowser(finalURL)
}

func resolveAppBrowser(baseDir string, customEnabled bool, customPath string) (browserPath, dirName string, err error) {
	if customEnabled {
		resolvedPath := browser.ResolvePath(customPath, baseDir)
		if !browser.IsExecutable(resolvedPath, baseDir) {
			return "", "", fmt.Errorf("%w: %s", ErrInvalidBrowserPath, customPath)
		}
		if browser.IsChromium(resolvedPath, baseDir) {
			return resolvedPath, browser.ResolveDataDir(customPath, baseDir), nil
		}
		slog.Warn("自定义浏览器不支持 App 模式，改用自动检测", "path", customPath)
	}

	browserPath, dirName = browser.DetectChromium()
	if browserPath == "" {
		return "", "", ErrBrowserNotFound
	}
	return browserPath, dirName, nil
}

func (m *Manager) startWebViewWindow(cfg Config, cacheRoot, finalURL, sig string, launchGen uint64) error {
	if m.isLaunchStale(launchGen) {
		return nil
	}
	if err := m.launchWebView2(cfg, cacheRoot, finalURL, launchGen); err != nil {
		m.cleanupWebView2()
		if errors.Is(err, errLaunchAborted) {
			return nil
		}
		return err
	}

	m.stateMu.Lock()
	if m.isLaunchStale(launchGen) {
		m.stateMu.Unlock()
		m.cleanupWebView2()
		return nil
	}
	m.activeSignature = sig
	m.stateMu.Unlock()
	return nil
}

func (m *Manager) startChromiumWindow(cfg Config, cacheRoot, browserPath, dirName, finalURL, appHostPort, sig string, launchGen uint64) error {
	if m.isLaunchStale(launchGen) {
		return nil
	}
	if err := m.launchChromiumApp(cfg, cacheRoot, browserPath, dirName, finalURL, appHostPort, launchGen); err != nil {
		m.cleanupChromium()
		if errors.Is(err, errLaunchAborted) {
			return nil
		}
		return err
	}

	m.stateMu.Lock()
	if m.isLaunchStale(launchGen) {
		m.stateMu.Unlock()
		m.cleanupChromium()
		return nil
	}
	m.activeSignature = sig
	m.stateMu.Unlock()
	return nil
}

func (m *Manager) tryReuseWindow(targetSig string) bool {
	if currSig := m.getActiveSignature(); currSig != "" && currSig != targetSig {
		m.cleanupSessions()
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

func (m *Manager) cleanupSessions() {
	m.cleanupWebView2()
	m.cleanupChromium()
	m.setActiveSignature("")
}

func (m *Manager) Cleanup() {
	m.cleanupGen.Add(1)
	m.cleanupSessions()
}

func (m *Manager) UpdateTheme(isDark bool) {
	m.wvMu.RLock()
	defer m.wvMu.RUnlock()

	if m.wv != nil {
		m.wv.SetDarkMode(isDark)
	}
}

func (m *Manager) Reload() error {
	m.wvMu.RLock()
	wv := m.wv
	m.wvMu.RUnlock()
	if wv != nil {
		return wv.Reload()
	}

	pid := m.isolatedPID.Load()
	if pid == 0 {
		return nil
	}

	m.stateMu.Lock()
	debugPort := m.debugPort
	exeName := m.isolatedExeName
	m.stateMu.Unlock()

	if debugPort == "" || !m.isChromiumProcessAlive(pid, exeName, debugPort) {
		return nil
	}
	return reloadCDPPageTargets(debugPort)
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

func (m *Manager) syncIsolatedProcess(debugPort string, livePID uint32) {
	if livePID == 0 {
		return
	}
	actualExe := sys.ProcessExeName(livePID)

	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	if debugPort != "" && m.debugPort != debugPort {
		return
	}
	m.isolatedPID.Store(livePID)
	if actualExe != "" {
		m.isolatedExeName = actualExe
	}
}

func (m *Manager) isChromiumProcessAlive(pid uint32, exeName, debugPort string) bool {
	if debugPort != "" {
		if livePID := sys.FindTCPListenerPID(debugPort); livePID != 0 {
			if livePID != pid {
				m.syncIsolatedProcess(debugPort, livePID)
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
		m.cleanupSessions()
		return false
	}

	sys.BringWindowToFront(hwnd)
	return true
}
