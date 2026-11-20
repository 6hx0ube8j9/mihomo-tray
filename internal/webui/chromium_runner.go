//go:build windows

package webui

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/fs"
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

func buildAppSignature(browserPath, dataDir string) string {
	cleanPath := strings.ToLower(filepath.Clean(strings.TrimSpace(browserPath)))
	cleanDir := strings.ToLower(strings.TrimSpace(dataDir))
	return fmt.Sprintf("%s|%s|%s", domain.WebOpenModeApp, cleanPath, cleanDir)
}

func resolveCustomBrowserDataDir(exePath string) string {
	if tag, ok := fs.MatchStandardChromium(exePath); ok {
		return tag
	}

	resolved := fs.ResolveBrowserPath(exePath)
	prefix := sanitizeBrowserPrefixResolved(resolved)
	normalized := strings.ToLower(filepath.Clean(resolved))
	sum := sha256.Sum256([]byte(normalized))

	var hexBuf [16]byte
	hex.Encode(hexBuf[:], sum[:8])

	return prefix + "_" + string(hexBuf[:])
}

func sanitizeBrowserPrefixResolved(resolvedPath string) string {
	if resolvedPath == "" {
		return "browser"
	}

	base := filepath.Base(resolvedPath)
	if ext := filepath.Ext(base); ext != "" {
		base = strings.TrimSuffix(base, ext)
	}
	base = strings.ToLower(base)

	var sb strings.Builder
	for i := 0; i < len(base); i++ {
		c := base[i]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			sb.WriteByte(c)
		}
	}

	if sb.Len() == 0 {
		return "browser"
	}
	return sb.String()
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

func (m *Manager) launchChromiumAppMode(cfg Config, cacheRoot, browserPath, dataDirName, finalURL, appHostPort string, eventCh chan<- Event) bool {
	if browserPath == "" || dataDirName == "" {
		return false
	}

	safeDebugPort := m.prepareDebugPort()

	if m.tryAttachExistingTarget(safeDebugPort, appHostPort, eventCh) {
		slog.Debug("复用现有独立浏览器窗口")
		return true
	}

	return m.launchIsolatedBrowser(cfg, cacheRoot, browserPath, dataDirName, finalURL, safeDebugPort, appHostPort, eventCh)
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

func (m *Manager) launchIsolatedBrowser(cfg Config, cacheRoot, browserPath, dataDirName, finalURL, debugPort, appHostPort string, eventCh chan<- Event) bool {
	slog.Debug("启动独立浏览器", "exe", browserPath, "dir", dataDirName, "port", debugPort)

	userDataDir := filepath.Join(cacheRoot, dataDirName)
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
	sys.AllowSetForegroundWindow(0)

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
