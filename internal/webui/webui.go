package webui

import (
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"mihomo-tray/internal/domain"
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
}

type Manager struct {
	debugPort   string
	isolatedPid atomic.Uint32
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

func (m *Manager) Launch(cfg Config, eventCh chan<- Event) {
	finalURL, appHostPort := buildFinalURL(cfg)

	if cfg.ForceSystemBrowser {
		slog.Info("根据全局配置，强制使用系统默认浏览器进入面板")
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

	safeDebugPort := m.prepareDebugPort()

	if m.tryAttachExistingTarget(safeDebugPort, appHostPort, eventCh) {
		return
	}

	browserPath, browserTag := DetectAvailableBrowser()
	if browserPath == "" {
		slog.Warn("未探测到受支持的浏览器，降级为默认浏览器打开")
		m.openSystemBrowser(finalURL, eventCh)
		return
	}

	m.launchIsolatedBrowser(cfg, browserPath, browserTag, finalURL, safeDebugPort, appHostPort, eventCh)
}

func (m *Manager) Cleanup() {
	sys.SetCachedWebUIHwnd(0)

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
	hwnd := sys.GetCachedWebUIHwnd()
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
	if hwnd := sys.GetCachedWebUIHwnd(); hwnd != 0 {
		if sys.IsWindowVisible(hwnd) {
			sys.FocusWindowSilky(hwnd)
			emitEvent(eventCh, EventReady)
			return true
		}
		sys.SetCachedWebUIHwnd(0)
	}
	return false
}

func (m *Manager) prepareDebugPort() string {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()

	if m.debugPort != "" && !IsDebugPortAlive(m.debugPort) {
		m.debugPort = ""
	}
	if m.debugPort == "" {
		m.debugPort = getCDPDebugPort()
	}
	return m.debugPort
}

func (m *Manager) tryAttachExistingTarget(debugPort, appHostPort string, eventCh chan<- Event) bool {
	targetID, targetTitle, found := GetWebUITarget(debugPort)
	if !found {
		return false
	}
	slog.Debug("发现存活的调试端口，尝试接管面板")
	_ = ActivateTarget(debugPort, targetID)
	return m.waitForWindow(debugPort, appHostPort, targetTitle, m.isolatedPid.Load(), eventCh)
}

func (m *Manager) launchIsolatedBrowser(cfg Config, browserPath, browserTag, finalURL, debugPort, appHostPort string, eventCh chan<- Event) {
	slog.Info("启动独立浏览器进程运行 WebUI", "Browser", browserTag, "DebugPort", debugPort)

	args := buildBrowserArgs(cfg, browserTag, finalURL, debugPort)
	cmd := exec.Command(browserPath, args...)
	if err := cmd.Start(); err != nil {
		slog.Error("创建浏览器进程失败", "err", err)
		emitEvent(eventCh, EventError)
		return
	}

	mainPid := uint32(cmd.Process.Pid)
	m.isolatedPid.Store(mainPid)
	go func() { _ = cmd.Wait() }()

	if m.waitForWindow(debugPort, appHostPort, "", mainPid, eventCh) {
		return
	}

	slog.Error("超时未能捕获浏览器窗口句柄")

	realPid := m.isolatedPid.Load()
	if realPid != 0 && sys.IsPidRunning(realPid, "") {
		slog.Warn("强制清理启动超时的失控浏览器进程", "PID", realPid)
		sys.HardKill(realPid)
	}
	m.isolatedPid.Store(0)
	emitEvent(eventCh, EventError)
}

func (m *Manager) waitForWindow(debugPort, appHostPort, targetTitle string, mainPid uint32, eventCh chan<- Event) bool {
	realBrowserPid := mainPid

	filterFn := isStandardBrowserWindow

	for i := 0; i < 30; i++ {
		time.Sleep(100 * time.Millisecond)

		if realBrowserPid == mainPid && i%5 == 0 {
			if truePid := sys.GetProcessIdByPort(debugPort); truePid != 0 {
				realBrowserPid = truePid
				m.isolatedPid.Store(realBrowserPid)
			}
		}

		liveTargetID, liveTitle, isLive := GetWebUITarget(debugPort)

		titleToSearch := liveTitle
		if titleToSearch == "" {
			titleToSearch = targetTitle
		}

		if isLive {
			_ = ActivateTarget(debugPort, liveTargetID)
			if sys.FindAndFocusAppWindow(titleToSearch, appHostPort, realBrowserPid, filterFn) {
				slog.Info("WebUI 窗口捕获成功")
				emitEvent(eventCh, EventReady)
				return true
			}
		} else if titleToSearch != "" || realBrowserPid != 0 {
			if sys.FindAndFocusAppWindow(titleToSearch, appHostPort, realBrowserPid, filterFn) {
				slog.Info("WebUI 窗口捕获成功(备用路径)")
				emitEvent(eventCh, EventReady)
				return true
			}
		}
	}
	return false
}

func buildBrowserArgs(cfg Config, browserTag, finalURL, debugPort string) []string {
	userDataDir := filepath.Join(cfg.BaseDir, domain.WebCacheDir, browserTag)
	_ = os.MkdirAll(userDataDir, 0755)
	winW, winH, winX, winY := sys.GetIdealWindowBounds()

	args := []string{
		"--app=" + finalURL,
		"--remote-debugging-port=" + debugPort,
		"--user-data-dir=" + userDataDir,
		"--window-size=" + strconv.Itoa(winW) + "," + strconv.Itoa(winH),
		"--window-position=" + strconv.Itoa(winX) + "," + strconv.Itoa(winY),
		"--no-first-run", "--no-default-browser-check", "--disable-extensions",
		"--disable-sync", "--disable-background-networking", "--disable-component-update",
		"--disk-cache-size=33554432", "--hide-crash-restore-bubble",
		"--disable-background-timer-throttling", "--disable-client-side-phishing-detection",
		"--disable-default-apps",

		// 禁用各类气泡与弹窗
		"--disable-save-password-bubble",
		"--deny-permission-prompts",
		"--disable-notifications",
		"--disable-search-engine-choice-screen",
		"--disable-features=Translate,LanguageDetection,PasswordLeakDetection,AutofillAddressProfileSavePrompt,AutofillCreditCardSavePrompt",
	}

	if p := strings.TrimSpace(cfg.ProxyPort); p != "" {
		args = append(args,
			"--proxy-server=127.0.0.1:"+p,
			"--proxy-bypass-list=127.0.0.1;localhost;<local>",
		)
	}
	return args
}
