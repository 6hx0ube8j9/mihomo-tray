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
	"sync"
	"time"

	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/sys"
)

const (
	browserDiskCacheBytes  = "33554432"
	wndClassChromiumPrefix = "Chrome_WidgetWin"
	chromiumInitTimeout    = 12 * time.Second
)

var ghostCharReplacer = strings.NewReplacer(
	"\u200b", "", "\u200c", "", "\u200d", "",
	"\u200e", "", "\u200f", "", "\ufeff", "", "\u00a0", " ",
)

var standardBrowserSuffixes = []string{
	"google chrome", "microsoft edge", "msedge",
	"brave", "vivaldi", "opera", "chromium",
}

var (
	browserCheckOnce  sync.Once
	cachedBrowserPath string
	cachedBrowserTag  string
)

func (m *Manager) launchChromiumAppMode(cfg Config, browserPath, dataDirName, finalURL, appHostPort string, eventCh chan<- Event) bool {
	if browserPath == "" || dataDirName == "" {
		return false
	}

	safeDebugPort := m.prepareDebugPort()

	if m.tryAttachExistingTarget(safeDebugPort, appHostPort, eventCh) {
		slog.Debug("复用现有独立浏览器窗口")
		return true
	}

	return m.launchIsolatedBrowser(cfg, browserPath, dataDirName, finalURL, safeDebugPort, appHostPort, eventCh)
}

func (m *Manager) prepareDebugPort() string {
	m.stateMu.Lock()
	port := m.debugPort
	m.stateMu.Unlock()

	if port != "" && !IsDebugPortAlive(port) {
		port = ""
	}

	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	if port == "" {
		m.debugPort = getCDPDebugPort()
	} else {
		m.debugPort = port
	}
	return m.debugPort
}

func (m *Manager) tryAttachExistingTarget(debugPort, appHostPort string, eventCh chan<- Event) bool {
	targetID, targetTitle, found := GetWebUITarget(debugPort)
	if !found {
		return false
	}
	slog.Debug("激活现有浏览器实例", "port", debugPort, "targetId", targetID)
	_ = ActivateTarget(debugPort, targetID)

	return m.waitForWindow(debugPort, appHostPort, targetTitle, m.isolatedPid.Load(), nil, eventCh)
}

func (m *Manager) launchIsolatedBrowser(cfg Config, browserPath, dataDirName, finalURL, debugPort, appHostPort string, eventCh chan<- Event) bool {
	slog.Debug("启动独立浏览器", "exe", browserPath, "dir", dataDirName, "port", debugPort)

	userDataDir := filepath.Join(cfg.BaseDir, domain.WebCacheDir, dataDirName)
	if err := os.MkdirAll(userDataDir, 0755); err != nil {
		slog.Error("创建缓存目录失败", "path", userDataDir, "err", err)
		return false
	}

	args := buildBrowserArgs(cfg, userDataDir, finalURL, debugPort)
	cmd := exec.Command(browserPath, args...)
	if err := cmd.Start(); err != nil {
		slog.Error("启动浏览器进程失败", "path", browserPath, "err", err)
		return false
	}

	mainPid := uint32(cmd.Process.Pid)
	m.isolatedPid.Store(mainPid)
	sys.AllowSetForegroundWindow(mainPid)

	exitCh := make(chan error, 1)
	go func() {
		exitCh <- cmd.Wait()
	}()

	if m.waitForWindow(debugPort, appHostPort, "", mainPid, exitCh, eventCh) {
		return true
	}

	slog.Warn("等待浏览器窗口就绪失败，清理残留进程")
	realPid := m.isolatedPid.Load()

	if realPid != 0 && realPid != mainPid && sys.IsPidRunning(realPid, "") {
		sys.HardKill(realPid)
	}

	if sys.IsPidRunning(mainPid, "") {
		sys.HardKill(mainPid)
	}

	m.isolatedPid.Store(0)
	return false
}

func (m *Manager) waitForWindow(debugPort, appHostPort, targetTitle string, mainPid uint32, exitCh <-chan error, eventCh chan<- Event) bool {
	realBrowserPid := mainPid
	var activatedTargetID string

	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()

	timer := time.NewTimer(chromiumInitTimeout)
	defer timer.Stop()

	for {
		select {
		case err, ok := <-exitCh:
			if !ok {
				exitCh = nil
				continue
			}

			if err != nil {
				slog.Warn("浏览器进程异常退出", "err", err)
				return false
			}

			slog.Debug("浏览器引导进程已退出，等待主窗口就绪")
			exitCh = nil

		case <-timer.C:
			slog.Error("等待浏览器窗口超时")
			return false

		case <-ticker.C:
			if realBrowserPid == mainPid {
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

			if isLive && liveTargetID != "" && liveTargetID != activatedTargetID {
				_ = ActivateTarget(debugPort, liveTargetID)
				activatedTargetID = liveTargetID
			}

			if isLive || titleToSearch != "" || realBrowserPid != 0 {
				if m.findAndFocusWebUI(titleToSearch, appHostPort, realBrowserPid) {
					emitEvent(eventCh, EventReady)
					return true
				}
			}
		}
	}
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

		if targetPid != 0 {
			if info.Pid == targetPid {
				if isTitleHit || isAnchorHit {
					return true
				}
				if pendingPidHwnd == 0 {
					pendingPidHwnd = info.Hwnd
				}
			}
			return false
		}

		if isStandardBrowserWindow(titleLower) {
			return false
		}
		return isTitleHit || isAnchorHit
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
		"--hide-crash-restore-bubble",
		"--disable-notifications",
		"--disable-search-engine-choice-screen",
		"--disk-cache-size=" + browserDiskCacheBytes,
		"--disable-features=Translate,LanguageDetection,PasswordLeakDetection,AutofillAddressProfileSavePrompt,AutofillCreditCardSavePrompt,PasswordManager,PasswordManagerEnabled",
	}

	if p := strings.TrimSpace(cfg.ProxyPort); p != "" && p != "0" {
		args = append(args,
			"--proxy-server="+net.JoinHostPort("127.0.0.1", p),
			"--proxy-bypass-list=127.0.0.1;localhost;<local>",
		)
	}
	return args
}

func detectAvailableBrowser() (path string, tag string) {
	browserCheckOnce.Do(func() {
		pf86 := os.Getenv("ProgramFiles(x86)")
		pf := os.Getenv("ProgramFiles")
		localApp := os.Getenv("LocalAppData")

		type browserCandidate struct {
			root string
			sub  string
			tag  string
		}

		candidates := []browserCandidate{
			{pf86, `Microsoft\Edge\Application\msedge.exe`, "edge"},
			{pf, `Microsoft\Edge\Application\msedge.exe`, "edge"},
			{localApp, `Microsoft\Edge\Application\msedge.exe`, "edge"},
			{pf, `Google\Chrome\Application\chrome.exe`, "chrome"},
			{pf86, `Google\Chrome\Application\chrome.exe`, "chrome"},
			{localApp, `Google\Chrome\Application\chrome.exe`, "chrome"},
			{pf, `BraveSoftware\Brave-Browser\Application\brave.exe`, "brave"},
			{localApp, `BraveSoftware\Brave-Browser\Application\brave.exe`, "brave"},
			{localApp, `Vivaldi\Application\vivaldi.exe`, "vivaldi"},
			{pf, `Vivaldi\Application\vivaldi.exe`, "vivaldi"},
			{pf86, `Vivaldi\Application\vivaldi.exe`, "vivaldi"},
		}

		for _, c := range candidates {
			if c.root == "" {
				continue
			}
			fullPath := filepath.Join(c.root, c.sub)
			if _, err := os.Stat(fullPath); err == nil {
				cachedBrowserPath = fullPath
				cachedBrowserTag = c.tag
				return
			}
		}
		slog.Debug("未检测到内置保底 Chromium 浏览器，后续将回退至系统默认浏览器")
	})

	return cachedBrowserPath, cachedBrowserTag
}