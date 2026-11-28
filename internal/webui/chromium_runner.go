//go:build windows

package webui

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

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

func (m *Manager) cleanupChromium() {
	m.stateMu.Lock()
	safeDebugPort := m.debugPort
	exeName := m.isolatedExeName
	m.debugPort = ""
	m.isolatedExeName = ""
	m.stateMu.Unlock()

	pid := m.isolatedPID.Swap(0)
	hadWindow := m.hwnd.Swap(0) != 0

	var targetPID uint32
	if safeDebugPort != "" {
		targetPID = sys.FindTCPListenerPID(safeDebugPort)
		if targetPID != 0 {
			if actualExe := sys.ProcessExeName(targetPID); actualExe != "" {
				exeName = actualExe
			}
			closeAllCDPTargets(safeDebugPort)
		}
	}
	if targetPID == 0 && !hadWindow && pid != 0 && exeName != "" {
		targetPID = pid
	}
	if targetPID == 0 {
		return
	}

	for i := 0; i < 15; i++ {
		if !sys.IsPidRunning(targetPID, exeName) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}

	if sys.IsPidRunning(targetPID, exeName) {
		sys.HardKill(targetPID)
	}
}

func (m *Manager) launchChromiumApp(cfg Config, cacheRoot, browserPath, dataDirName, finalURL, appHostPort string, launchGen uint64) error {
	if browserPath == "" || dataDirName == "" {
		return errors.New("未找到可用浏览器")
	}
	if m.isLaunchStale(launchGen) {
		return errLaunchAborted
	}

	safeDebugPort := m.prepareDebugPort()
	if m.tryAttachExistingWindow(safeDebugPort, appHostPort, launchGen) {
		return nil
	}

	return m.startBrowserProcess(cfg, cacheRoot, browserPath, dataDirName, finalURL, safeDebugPort, appHostPort, launchGen)
}

func (m *Manager) prepareDebugPort() string {
	m.stateMu.Lock()
	port := m.debugPort
	m.stateMu.Unlock()

	if port != "" && (sys.FindTCPListenerPID(port) == 0 || !isCDPAlive(port)) {
		port = ""
	}

	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	if port == "" {
		m.debugPort = allocateCDPPort()
	} else {
		m.debugPort = port
	}
	return m.debugPort
}

func (m *Manager) tryAttachExistingWindow(debugPort, appHostPort string, launchGen uint64) bool {
	if m.isLaunchStale(launchGen) || sys.FindTCPListenerPID(debugPort) == 0 {
		return false
	}

	targetID, targetTitle, found := findCDPPageTarget(debugPort)
	if !found {
		return false
	}
	_ = activateCDPTarget(debugPort, targetID)

	return m.waitForWindow(debugPort, appHostPort, targetTitle, m.isolatedPID.Load(), nil, launchGen) == nil
}

func (m *Manager) startBrowserProcess(cfg Config, cacheRoot, browserPath, dataDirName, finalURL, debugPort, appHostPort string, launchGen uint64) error {
	if m.isLaunchStale(launchGen) {
		return errLaunchAborted
	}

	userDataDir := filepath.Join(cacheRoot, dataDirName)
	if err := os.MkdirAll(userDataDir, 0755); err != nil {
		return fmt.Errorf("创建浏览器缓存目录失败: %w", err)
	}

	exeName := filepath.Base(browserPath)
	args := buildBrowserArgs(cfg, userDataDir, finalURL, debugPort)
	cmd := exec.Command(browserPath, args...)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动浏览器进程失败: %w", err)
	}

	mainPID := uint32(cmd.Process.Pid)
	m.stateMu.Lock()
	m.isolatedExeName = exeName
	m.stateMu.Unlock()
	m.isolatedPID.Store(mainPID)
	sys.AllowSetForegroundWindow(0)

	exitCh := make(chan error, 1)
	go func() {
		exitCh <- cmd.Wait()
	}()

	err := m.waitForWindow(debugPort, appHostPort, "", mainPID, exitCh, launchGen)
	if err == nil {
		return nil
	}

	realPID := m.isolatedPID.Swap(0)
	m.hwnd.Store(0)
	m.stateMu.Lock()
	realExeName := m.isolatedExeName
	if m.debugPort == debugPort {
		m.debugPort = ""
		m.isolatedExeName = ""
	}
	m.stateMu.Unlock()

	if realExeName == "" {
		realExeName = exeName
	}
	if realPID != 0 && realPID != mainPID && sys.IsPidRunning(realPID, realExeName) {
		sys.HardKill(realPID)
	}
	if sys.IsPidRunning(mainPID, exeName) {
		sys.HardKill(mainPID)
	}

	return err
}

func (m *Manager) waitForWindow(debugPort, appHostPort, targetTitle string, mainPID uint32, exitCh <-chan error, launchGen uint64) error {
	browserPID := mainPID
	pidResolved := false
	var activatedTargetID string

	ticker := time.NewTicker(80 * time.Millisecond)
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
				return fmt.Errorf("浏览器进程异常退出: %w", err)
			}
			exitCh = nil

		case <-timer.C:
			return fmt.Errorf("等待浏览器窗口超时 (%s)", chromiumInitTimeout)

		case <-ticker.C:
			if m.isLaunchStale(launchGen) {
				return errLaunchAborted
			}

			if !pidResolved {
				if livePID := sys.FindTCPListenerPID(debugPort); livePID != 0 {
					browserPID = livePID
					m.syncIsolatedProcess(debugPort, livePID)
					pidResolved = true
				}
			}

			var liveTargetID, liveTitle string
			var isLive bool
			if pidResolved {
				liveTargetID, liveTitle, isLive = findCDPPageTarget(debugPort)
				if isLive && liveTargetID != "" && liveTargetID != activatedTargetID {
					_ = activateCDPTarget(debugPort, liveTargetID)
					activatedTargetID = liveTargetID
				}
			}

			titleToSearch := liveTitle
			if titleToSearch == "" {
				titleToSearch = targetTitle
			}

			if isLive || titleToSearch != "" || browserPID != 0 {
				if m.findAndFocusWindow(titleToSearch, appHostPort, browserPID) {
					return nil
				}
			}
		}
	}
}

func (m *Manager) findAndFocusWindow(targetTitle, appHostPort string, targetPID uint32) bool {
	exactTitle := strings.TrimSpace(targetTitle)
	fallbackAnchor := strings.ToLower(strings.TrimSpace(appHostPort))

	var pendingPIDHwnd uintptr

	targetHwnd := sys.FindTopWindow(func(info sys.WindowInfo) bool {
		if !sys.IsWindowVisible(info.Hwnd) || !strings.HasPrefix(info.ClassName, wndClassChromiumPrefix) || info.Title == "" {
			return false
		}

		titleLower := strings.ToLower(info.Title)
		isTitleHit := exactTitle != "" && info.Title == exactTitle
		isAnchorHit := fallbackAnchor != "" && strings.Contains(titleLower, fallbackAnchor)

		if targetPID != 0 {
			if info.Pid == targetPID {
				if isTitleHit || isAnchorHit {
					return true
				}
				if pendingPIDHwnd == 0 {
					pendingPIDHwnd = info.Hwnd
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
		targetHwnd = pendingPIDHwnd
	}

	if targetHwnd != 0 {
		m.hwnd.Store(targetHwnd)
		sys.BringWindowToFront(targetHwnd)
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

	args = append(args, buildProxyArgs(cfg.ProxyPort)...)
	return args
}
