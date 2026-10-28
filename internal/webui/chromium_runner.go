//go:build windows

package webui

import (
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/sys"
)

const (
	browserDiskCacheBytes  = "33554432"
	wndClassChromiumPrefix = "Chrome_WidgetWin"
)

var ghostCharReplacer = strings.NewReplacer(
	"\u200b", "", "\u200c", "", "\u200d", "",
	"\u200e", "", "\u200f", "", "\ufeff", "", "\u00a0", " ",
)

var standardBrowserSuffixes = []string{
	"google chrome", "microsoft edge", "msedge",
	"brave", "vivaldi", "opera", "chromium",
}

func (m *Manager) launchChromiumAppMode(cfg Config, finalURL, appHostPort string, eventCh chan<- Event) bool {
	browserPath, browserTag := detectAvailableBrowser()
	if browserPath == "" {
		return false
	}

	safeDebugPort := m.prepareDebugPort()

	if m.tryAttachExistingTarget(safeDebugPort, appHostPort, eventCh) {
		slog.Info("已复用现有的浏览器面板")
		return true
	}

	m.launchIsolatedBrowser(cfg, browserPath, browserTag, finalURL, safeDebugPort, appHostPort, eventCh)
	return true
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
	slog.Debug("检测到调试端口存活，尝试激活窗口")
	_ = ActivateTarget(debugPort, targetID)
	return m.waitForWindow(debugPort, appHostPort, targetTitle, m.isolatedPid.Load(), eventCh)
}

func (m *Manager) launchIsolatedBrowser(cfg Config, browserPath, browserTag, finalURL, debugPort, appHostPort string, eventCh chan<- Event) {
	slog.Info("启动独立浏览器窗口", "browser", browserTag, "port", debugPort)

	userDataDir := filepath.Join(cfg.BaseDir, domain.WebCacheDir, browserTag)
	if err := os.MkdirAll(userDataDir, 0755); err != nil {
		slog.Warn("创建用户数据目录失败", "path", userDataDir, "err", err)
	}

	args := buildBrowserArgs(cfg, userDataDir, finalURL, debugPort)
	cmd := exec.Command(browserPath, args...)
	if err := cmd.Start(); err != nil {
		slog.Error("启动浏览器进程失败", "err", err)
		emitEvent(eventCh, EventError)
		return
	}

	mainPid := uint32(cmd.Process.Pid)
	m.isolatedPid.Store(mainPid)
	go func() { _ = cmd.Wait() }()

	if m.waitForWindow(debugPort, appHostPort, "", mainPid, eventCh) {
		return
	}

	slog.Error("等待浏览器窗口就绪超时")
	realPid := m.isolatedPid.Load()
	if realPid != 0 && sys.IsPidRunning(realPid, "") {
		slog.Warn("结束超时的浏览器进程", "pid", realPid)
		sys.HardKill(realPid)
	}
	m.isolatedPid.Store(0)
	emitEvent(eventCh, EventError)
}

func (m *Manager) waitForWindow(debugPort, appHostPort, targetTitle string, mainPid uint32, eventCh chan<- Event) bool {
	realBrowserPid := mainPid

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
		}

		if isLive || titleToSearch != "" || realBrowserPid != 0 {
			if m.findAndFocusWebUI(titleToSearch, appHostPort, realBrowserPid) {
				slog.Info("浏览器面板窗口已就绪")
				emitEvent(eventCh, EventReady)
				return true
			}
		}
	}
	return false
}

func (m *Manager) findAndFocusWebUI(targetTitle, appHostPort string, targetPid uint32) bool {
	exactTitle := strings.TrimSpace(targetTitle)
	fallbackAnchor := strings.ToLower(strings.TrimSpace(appHostPort))

	var pendingPidHwnd uintptr

	targetHwnd := sys.FindTopWindow(func(info sys.WindowInfo) bool {
		if !sys.IsWindowVisible(info.Hwnd) || !strings.HasPrefix(info.ClassName, wndClassChromiumPrefix) || info.Title == "" {
			return false
		}

		titleLower := strings.ToLower(info.Title)
		isTitleHit := exactTitle != "" && info.Title == exactTitle
		isAnchorHit := fallbackAnchor != "" && strings.Contains(titleLower, fallbackAnchor)

		if targetPid != 0 && info.Pid == targetPid {
			if isTitleHit || isAnchorHit {
				return true
			}
			if pendingPidHwnd == 0 {
				pendingPidHwnd = info.Hwnd
			}
			return false
		}

		if targetPid == 0 {
			if isStandardBrowserWindow(titleLower) {
				return false
			}
			return isTitleHit || isAnchorHit
		}

		return false
	})

	if targetHwnd == 0 {
		targetHwnd = pendingPidHwnd
	}

	if targetHwnd != 0 {
		m.hwnd.Store(targetHwnd)
		sys.FocusWindowSilky(targetHwnd)
		return true
	}
	return false
}

func isStandardBrowserWindow(title string) bool {
	clean := ghostCharReplacer.Replace(strings.ToLower(title))
	clean = strings.TrimSpace(clean)
	for _, suffix := range standardBrowserSuffixes {
		if strings.HasSuffix(clean, suffix) {
			return true
		}
	}
	return false
}

func buildBrowserArgs(cfg Config, userDataDir, finalURL, debugPort string) []string {
	winW, winH, winX, winY := calculateWindowBounds()

	args := []string{
		"--app=" + finalURL,
		"--remote-debugging-port=" + debugPort,
		"--user-data-dir=" + userDataDir,
		"--window-size=" + strconv.Itoa(winW) + "," + strconv.Itoa(winH),
		"--window-position=" + strconv.Itoa(winX) + "," + strconv.Itoa(winY),
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-extensions",
		"--disable-sync",
		"--disable-background-networking",
		"--disable-component-update",
		"--disk-cache-size=" + browserDiskCacheBytes,
		"--hide-crash-restore-bubble",
		"--disable-background-timer-throttling",
		"--disable-client-side-phishing-detection",
		"--disable-default-apps",
		"--disable-save-password-bubble",
		"--deny-permission-prompts",
		"--disable-notifications",
		"--disable-search-engine-choice-screen",
		"--disable-features=Translate,LanguageDetection,PasswordLeakDetection,AutofillAddressProfileSavePrompt,AutofillCreditCardSavePrompt",
	}

	if p := strings.TrimSpace(cfg.ProxyPort); p != "" {
		args = append(args,
			"--proxy-server="+net.JoinHostPort("127.0.0.1", p),
			"--proxy-bypass-list=127.0.0.1;localhost;<local>",
		)
	}
	return args
}

func detectAvailableBrowser() (path string, tag string) {
	type browserInfo struct {
		path string
		tag  string
	}
	var potentialBrowsers = []browserInfo{
		{filepath.Join(os.Getenv("ProgramFiles(x86)"), `Microsoft\Edge\Application\msedge.exe`), "edge"},
		{filepath.Join(os.Getenv("ProgramFiles"), `Microsoft\Edge\Application\msedge.exe`), "edge"},
		{filepath.Join(os.Getenv("LocalAppData"), `Microsoft\Edge\Application\msedge.exe`), "edge"},
		{filepath.Join(os.Getenv("ProgramFiles"), `Google\Chrome\Application\chrome.exe`), "chrome"},
		{filepath.Join(os.Getenv("ProgramFiles(x86)"), `Google\Chrome\Application\chrome.exe`), "chrome"},
		{filepath.Join(os.Getenv("LocalAppData"), `Google\Chrome\Application\chrome.exe`), "chrome"},
		{filepath.Join(os.Getenv("ProgramFiles"), `BraveSoftware\Brave-Browser\Application\brave.exe`), "brave"},
		{filepath.Join(os.Getenv("LocalAppData"), `BraveSoftware\Brave-Browser\Application\brave.exe`), "brave"},
		{filepath.Join(os.Getenv("LocalAppData"), `Vivaldi\Application\vivaldi.exe`), "vivaldi"},
		{filepath.Join(os.Getenv("ProgramFiles"), `Vivaldi\Application\vivaldi.exe`), "vivaldi"},
		{filepath.Join(os.Getenv("ProgramFiles(x86)"), `Vivaldi\Application\vivaldi.exe`), "vivaldi"},
	}

	for _, b := range potentialBrowsers {
		if b.path != "" {
			if _, err := os.Stat(b.path); err == nil {
				return b.path, b.tag
			}
		}
	}
	return "", ""
}
