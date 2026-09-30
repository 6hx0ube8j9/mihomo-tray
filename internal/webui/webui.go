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
}

type Manager struct {
	debugPort   string
	isolatedPid atomic.Uint32
	mu          sync.Mutex
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

	if !m.mu.TryLock() {
		return
	}
	defer m.mu.Unlock()

	safeDebugPort := m.prepareDebugPort()

	if m.tryAttachExistingTarget(safeDebugPort, appHostPort, eventCh) {
		return
	}

	if m.launchIsolatedBrowser(cfg, finalURL, safeDebugPort, appHostPort, eventCh) {
		return
	}

	slog.Warn("未探测到受支持的独立浏览器或启动失败，降级为默认浏览器打开")
	m.openSystemBrowser(finalURL, eventCh)
}

func (m *Manager) Cleanup() {
	sys.SetCachedWebUIHwnd(0)

	m.mu.Lock()
	safeDebugPort := m.debugPort
	m.mu.Unlock()

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
	if m.debugPort != "" && !IsDebugPortAlive(m.debugPort) {
		m.debugPort = ""
	}
	if m.debugPort == "" {
		m.debugPort = getCDPDebugPort()
	}
	return m.debugPort
}

func (m *Manager) tryAttachExistingTarget(debugPort, appHostPort string, eventCh chan<- Event) bool {
	if _, _, found := GetWebUITarget(debugPort); !found {
		return false
	}
	slog.Debug("发现存活的调试端口，尝试接管面板")
	return m.waitForWindow(debugPort, appHostPort, m.isolatedPid.Load(), eventCh)
}

func (m *Manager) launchIsolatedBrowser(cfg Config, finalURL, debugPort, appHostPort string, eventCh chan<- Event) bool {
	browserPath, browserTag := DetectAvailableBrowser()
	if browserPath == "" {
		return false
	}

	slog.Info("启动独立浏览器进程运行 WebUI", "Browser", browserTag, "DebugPort", debugPort)
	
	args := buildBrowserArgs(cfg, browserTag, finalURL, debugPort)
	cmd := exec.Command(browserPath, args...)
	if err := cmd.Start(); err != nil {
		slog.Error("创建浏览器进程失败", "err", err)
		return false
	}

	mainPid := uint32(cmd.Process.Pid)
	m.isolatedPid.Store(mainPid)
	go func() { _ = cmd.Wait() }()

	if m.waitForWindow(debugPort, appHostPort, mainPid, eventCh) {
		return true
	}

	slog.Error("超时未能捕获浏览器窗口句柄")
	if sys.IsPidRunning(mainPid, "") {
		sys.HardKill(mainPid)
	}
	m.isolatedPid.Store(0)
	return false
}

func (m *Manager) waitForWindow(debugPort, appHostPort string, mainPid uint32, eventCh chan<- Event) bool {
	realBrowserPid := mainPid
	for i := 0; i < 30; i++ {
		time.Sleep(100 * time.Millisecond)
		
		if i%5 == 0 {
			if truePid := sys.GetProcessIdByPort(debugPort); truePid != 0 {
				realBrowserPid = truePid
				m.isolatedPid.Store(realBrowserPid)
			}
		}

		liveTargetID, liveTitle, isLive := GetWebUITarget(debugPort)
		if isLive {
			_ = ActivateTarget(debugPort, liveTargetID)
			
			if sys.FindAndFocusAppWindow(liveTitle, appHostPort, realBrowserPid, isStandardBrowserWindow) {
				slog.Info("WebUI 窗口捕获成功")
				emitEvent(eventCh, EventReady)
				return true
			}
		}
	}
	return false
}

func buildBrowserArgs(cfg Config, browserTag, finalURL, debugPort string) []string {
	userDataDir := filepath.Join(cfg.BaseDir, "webcache", browserTag)
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
		"--disk-cache-size=33554432", "--disable-translate", "--hide-crash-restore-bubble",
		"--disable-background-timer-throttling", "--disable-client-side-phishing-detection",
		"--disable-default-apps",
	}

	if p := strings.TrimSpace(cfg.ProxyPort); p != "" {
		args = append(args,
			"--proxy-server=127.0.0.1:"+p,
			"--proxy-bypass-list=127.0.0.1;localhost;<local>",
		)
	}
	return args
}
