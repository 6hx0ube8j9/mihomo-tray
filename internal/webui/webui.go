package webui

import (
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"strings"
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
	APIAddr           string
	Secret            string
	ProxyPort         string
	BaseDir           string
	UIName            string
	WebOpenMode       string
	RemoteWebUI       bool
	RemoteWebUIURL    string
	CustomBrowserPath string
	CustomUserDataDir string
	IsDark            bool
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

	envCheckMu    sync.Mutex
	envCheckErr   error
	envCheckedDir string
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

	if baseDir != "" && m.envCheckedDir == baseDir {
		return m.envCheckErr
	}

	m.envCheckedDir = baseDir
	m.envCheckErr = nil

	cacheBaseDir := filepath.Join(baseDir, domain.WebCacheDir)
	err := sys.ValidateDirWritable(cacheBaseDir)
	if err == nil {
		return nil
	}

	if errors.Is(err, sys.ErrSystemProtectedDir) ||
		errors.Is(err, sys.ErrOtherUserProfileDir) ||
		errors.Is(err, sys.ErrInvalidPath) {
		m.envCheckErr = err
		return err
	}

	time.Sleep(100 * time.Millisecond)
	m.envCheckErr = sys.ValidateDirWritable(cacheBaseDir)

	return m.envCheckErr
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
		m.launchBrowserMode(customPath, finalURL, eventCh)

	case domain.WebOpenModeWebView:
		m.launchPureWebViewMode(cfg, finalURL, eventCh)

	case domain.WebOpenModeApp:
		m.launchAppMode(cfg, customPath, finalURL, appHostPort, eventCh)

	default:
		m.launchAutoMode(cfg, finalURL, appHostPort, eventCh)
	}
}

func (m *Manager) launchBrowserMode(customPath, finalURL string, eventCh chan<- Event) {
	if m.IsActive() {
		m.Cleanup()
	}

	if customPath == "" {
		slog.Debug("启动系统默认浏览器")
		m.openSystemBrowser(finalURL, eventCh)
		return
	}

	if !domain.IsValidBrowserExecutable(customPath) {
		slog.Error("自定义浏览器路径无效或文件不存在", "path", customPath)
		emitEvent(eventCh, EventError)
		return
	}

	slog.Debug("启动自定义浏览器打开标签页", "path", customPath)
	m.openCustomBrowserTab(customPath, finalURL, eventCh)
}

func (m *Manager) launchPureWebViewMode(cfg Config, finalURL string, eventCh chan<- Event) {
	if err := m.checkSandboxedEnvironment(cfg.BaseDir); err != nil {
		slog.Error("缓存目录不可写，无法启动内置 WebView2 面板", "err", err)
		emitEvent(eventCh, EventError)
		return
	}

	const sig = domain.WebOpenModeWebView
	m.reconcileSessionLocked(sig)

	if m.tryWakeCachedWindow(eventCh) {
		return
	}

	if m.launchWebView2(cfg, finalURL, eventCh) {
		m.setActiveSignature(sig)
		slog.Debug("内置面板已启动", "mode", sig)
		return
	}

	slog.Error("内置 WebView2 面板启动失败")
	m.cleanupWebView2()
	emitEvent(eventCh, EventError)
}

func (m *Manager) launchAppMode(cfg Config, customPath, finalURL, appHostPort string, eventCh chan<- Event) {
	if err := m.checkSandboxedEnvironment(cfg.BaseDir); err != nil {
		slog.Error("缓存目录不可写，无法启动独立浏览器面板", "err", err)
		emitEvent(eventCh, EventError)
		return
	}

	var browserPath, dirName string
	if customPath != "" {
		if !domain.IsValidBrowserExecutable(customPath) {
			slog.Error("自定义浏览器路径无效或文件不存在", "path", customPath)
			emitEvent(eventCh, EventError)
			return
		}
		browserPath = customPath
		dirName = resolveCustomUserDataDir(customPath, cfg.CustomUserDataDir)
	} else {
		browserPath, dirName = detectAvailableBrowser()
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

	if m.launchChromiumAppMode(cfg, browserPath, dirName, finalURL, appHostPort, eventCh) {
		m.setActiveSignature(sig)
		slog.Debug("独立浏览器面板已启动", "exe", browserPath, "dir", dirName)
		return
	}

	slog.Error("独立浏览器面板启动失败", "exe", browserPath, "dir", dirName)
	m.cleanupChromium()
	emitEvent(eventCh, EventError)
}

func (m *Manager) launchAutoMode(cfg Config, finalURL, appHostPort string, eventCh chan<- Event) {
	if err := m.checkSandboxedEnvironment(cfg.BaseDir); err != nil {
		slog.Warn("缓存目录不可写，使用系统浏览器", "err", err)
		m.openSystemBrowser(finalURL, eventCh)
		return
	}

	const sig = domain.WebOpenModeAuto
	m.reconcileSessionLocked(sig)

	if m.tryWakeCachedWindow(eventCh) {
		return
	}

	if m.launchWebView2(cfg, finalURL, eventCh) {
		m.setActiveSignature(sig)
		slog.Debug("内置面板已启动", "mode", sig)
		return
	}
	slog.Warn("内置面板启动失败，切换独立浏览器")
	m.cleanupWebView2()

	if browserPath, browserTag := detectAvailableBrowser(); browserPath != "" {
		if m.launchChromiumAppMode(cfg, browserPath, browserTag, finalURL, appHostPort, eventCh) {
			m.setActiveSignature(sig)
			slog.Debug("独立浏览器已启动", "browser", browserTag)
			return
		}
	}
	slog.Warn("独立浏览器启动失败，切换系统浏览器")
	m.cleanupChromium()

	m.openSystemBrowser(finalURL, eventCh)
}

func buildAppSignature(browserPath, dataDir string) string {
	cleanPath := strings.ToLower(filepath.Clean(strings.TrimSpace(browserPath)))
	cleanDir := strings.ToLower(strings.TrimSpace(dataDir))
	return fmt.Sprintf("%s|%s|%s", domain.WebOpenModeApp, cleanPath, cleanDir)
}

func resolveCustomUserDataDir(customBrowserPath, rawDir string) string {
	trimmed := strings.TrimSpace(rawDir)
	if trimmed != "" {
		base := filepath.Base(trimmed)
		if domain.IsValidUserDataDirName(base) {
			if base != trimmed {
				slog.Warn("剥离自定义数据目录路径层级", "raw", rawDir, "dir", base)
			}
			return base
		}
	}

	fallback := domain.GenerateDeterministicUserDataDir(customBrowserPath)
	slog.Warn("自定义数据目录未配置或包含非法字符，使用确定性目录名兜底", "dir", fallback)
	return fallback
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
		for i := 0; i < 15; i++ {
			if !sys.IsPidRunning(pid, "") {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if sys.IsPidRunning(pid, "") {
			slog.Debug("清理残留浏览器进程", "pid", pid)
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

func (m *Manager) TryAcquireMaintenance() (unlock func(), ok bool) {
	if m.IsActive() {
		return nil, false
	}
	if !m.launchMu.TryLock() {
		return nil, false
	}
	if m.IsActive() {
		m.launchMu.Unlock()
		return nil, false
	}

	var once sync.Once
	safeUnlock := func() {
		once.Do(m.launchMu.Unlock)
	}
	return safeUnlock, true
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

	if !sys.IsWindowAliveAndResponsive(hwnd) {
		slog.Debug("窗口失效，正在重置")
		m.Cleanup()
		return false
	}

	sys.FocusWindowSilky(hwnd)
	emitEvent(eventCh, EventReady)
	return true
}