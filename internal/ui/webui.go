package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"log/slog"

	"mihomo-tray/internal/sys"
)

type Event int

const (
	EventReady Event = iota
	EventError
)

type Config struct {
	APIAddr   string
	Secret    string
	ProxyPort string
	BaseDir   string
	UIName    string
}

var (
	chromeDebugPort string
	isolatedWebUIPid uint32
	debugPortMu     sync.Mutex
	launchMu        sync.Mutex

	webuiClient = &http.Client{
		Transport: &http.Transport{
			DisableKeepAlives: true,
		},
	}
)

func isDebugPortAlive(port string) bool {
	resp, err := safeGet(fmt.Sprintf("http://127.0.0.1:%s/json", port))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	var targets []map[string]interface{}
	return json.NewDecoder(resp.Body).Decode(&targets) == nil
}

func getFreePort() string {
	addr, err := net.ResolveTCPAddr("tcp", "127.0.0.1:0")
	if err != nil {
		return "52719"
	}
	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return "52719"
	}
	port := strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
	_ = l.Close()
	return port
}

func emitEvent(ch chan<- Event, event Event) {
	if ch == nil {
		return
	}
	select {
	case ch <- event:
	default:
	}
}

func safeGet(url string) (*http.Response, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	return webuiClient.Do(req)
}

func getWebUITarget(debugPort string) (id string, title string, found bool) {
	resp, err := safeGet(fmt.Sprintf("http://127.0.0.1:%s/json", debugPort))
	if err != nil {
		return "", "", false
	}
	defer resp.Body.Close()

	var targets []map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&targets); err != nil {
		return "", "", false
	}

	for _, t := range targets {
		pURL, _ := t["url"].(string)
		if strings.Contains(pURL, "/ui/") || strings.Contains(pURL, "setup") || strings.Contains(pURL, "#/proxies") {
			id, _ = t["id"].(string)
			title, _ = t["title"].(string)
			return id, title, true
		}
	}
	return "", "", false
}

func Launch(cfg Config, eventCh chan<- Event) {
	cleanAddr := strings.TrimRight(cfg.APIAddr, "/")
	cleanAddr = strings.TrimPrefix(strings.TrimPrefix(cleanAddr, "http://"), "https://")

	host, port, err := net.SplitHostPort(cleanAddr)
	if err != nil {
		host = cleanAddr
		port = "9090"
	}
	if port == "" {
		port = "9090"
	}

	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		host = "127.0.0.1"
	}

	uiPath := "/ui/"
	if cfg.UIName != "" {
		uiPath = fmt.Sprintf("/ui/%s/", strings.Trim(cfg.UIName, "/"))
	}

	query := fmt.Sprintf("hostname=%s&port=%s", host, port)
	if cfg.Secret != "" {
		query += fmt.Sprintf("&secret=%s", url.QueryEscape(cfg.Secret))
	}

	finalURL := fmt.Sprintf("http://%s:%s%s?%s#/?%s", host, port, uiPath, query, query)
	proxyAddr := "127.0.0.1:" + cfg.ProxyPort

	if hwnd := sys.GetCachedWebUIHwnd(); hwnd != 0 {
		if sys.IsWindowVisible(hwnd) {
			slog.Debug("唤醒已隐藏的 WebUI 窗口")
			sys.FocusWindowSilky(hwnd)
			emitEvent(eventCh, EventReady)
			return
		}
		sys.SetCachedWebUIHwnd(0)
	}

	if !launchMu.TryLock() {
		return
	}
	defer launchMu.Unlock()

	debugPortMu.Lock()
	if chromeDebugPort != "" && !isDebugPortAlive(chromeDebugPort) {
		chromeDebugPort = ""
	}
	if chromeDebugPort == "" {
		chromeDebugPort = getFreePort()
	}	
	safeDebugPort := chromeDebugPort
	debugPortMu.Unlock()
	
	targetID, targetTitle, found := getWebUITarget(safeDebugPort)
	if found {
		slog.Debug("发现存活的调试端口，尝试直接激活标签页", "Port", safeDebugPort, "ID", targetID)
		if actResp, actErr := safeGet(fmt.Sprintf("http://127.0.0.1:%s/json/activate/%s", safeDebugPort, targetID)); actErr == nil {
			_ = actResp.Body.Close()
		}

		if targetTitle != "" {
			windowFound := false
			for i := 0; i < 5; i++ {
				if sys.FindAndFocusAppWindow(targetTitle, 0) {
					windowFound = true
					break
				}
				time.Sleep(50 * time.Millisecond)
			}
			if windowFound {
				emitEvent(eventCh, EventReady)
				return
			}
		}
	}

	type browserInfo struct {
		path string
		tag  string
	}
	potentialBrowsers := []browserInfo{
		{filepath.Join(os.Getenv("ProgramFiles(x86)"), `Microsoft\Edge\Application\msedge.exe`), "edge"},
		{filepath.Join(os.Getenv("ProgramFiles"), `Microsoft\Edge\Application\msedge.exe`), "edge"},
		{filepath.Join(os.Getenv("ProgramFiles"), `Google\Chrome\Application\chrome.exe`), "chrome"},
		{filepath.Join(os.Getenv("ProgramFiles(x86)"), `Google\Chrome\Application\chrome.exe`), "chrome"},
		{filepath.Join(os.Getenv("LocalAppData"), `Google\Chrome\Application\chrome.exe`), "chrome"},
		{filepath.Join(os.Getenv("ProgramFiles"), `BraveSoftware\Brave-Browser\Application\brave.exe`), "brave"},
		{filepath.Join(os.Getenv("LocalAppData"), `BraveSoftware\Brave-Browser\Application\brave.exe`), "brave"},
		{filepath.Join(os.Getenv("LocalAppData"), `Vivaldi\Application\vivaldi.exe`), "vivaldi"},
		{filepath.Join(os.Getenv("ProgramFiles"), `Vivaldi\Application\vivaldi.exe`), "vivaldi"},
		{filepath.Join(os.Getenv("ProgramFiles(x86)"), `Vivaldi\Application\vivaldi.exe`), "vivaldi"},
	}

	var browserPath, browserTag string
	for _, b := range potentialBrowsers {
		if _, err := os.Stat(b.path); err == nil {
			browserPath = b.path
			browserTag = b.tag
			break
		}
	}

	if browserPath != "" {
		slog.Info("准备注入沙盒启动 WebUI", "Browser", browserTag, "DebugPort", safeDebugPort)
		userDataDir := filepath.Join(cfg.BaseDir, "webcache", browserTag)
		_ = os.MkdirAll(userDataDir, 0755)
		winW, winH, winX, winY := sys.GetIdealWindowBounds()

		args := []string{
			"--app=" + finalURL,
			"--remote-debugging-port=" + safeDebugPort,
			"--user-data-dir=" + userDataDir,
			"--window-size=" + strconv.Itoa(winW) + "," + strconv.Itoa(winH),
			"--window-position=" + strconv.Itoa(winX) + "," + strconv.Itoa(winY),
			"--proxy-server=" + proxyAddr,
			"--no-first-run",
			"--no-default-browser-check",
		}

		cmd := exec.Command(browserPath, args...)
		if err := cmd.Start(); err == nil {
			mainPid := uint32(cmd.Process.Pid)
			atomic.StoreUint32(&isolatedWebUIPid, mainPid)
			slog.Debug("独立浏览器进程已启动", "PID", mainPid)

			go func() {
				_ = cmd.Wait()
			}()

			for i := 0; i < 30; i++ {
				time.Sleep(100 * time.Millisecond)
				_, liveTitle, isLive := getWebUITarget(safeDebugPort)
				if isLive {
					if sys.FindAndFocusAppWindow(liveTitle, mainPid) {
						slog.Info("WebUI 窗口捕获成功")
						emitEvent(eventCh, EventReady)
						return
					}
				}
			}
			slog.Error("超时未能捕获浏览器窗口句柄")
			emitEvent(eventCh, EventError)
			return

		} else {
			slog.Error("创建浏览器进程失败", "err", err)
			emitEvent(eventCh, EventError)
			return
		}
	} else {
		slog.Warn("未探测到受支持的浏览器，降级为系统默认打开")
		err := exec.Command("cmd", "/c", "start", "", finalURL).Start()
		if err == nil {
			emitEvent(eventCh, EventReady)
		} else {
			slog.Error("调用系统默认浏览器失败", "err", err)
			emitEvent(eventCh, EventError)
		}
		return
	}
}

func Cleanup() {
	debugPortMu.Lock()
	safeDebugPort := chromeDebugPort
	debugPortMu.Unlock()
	if safeDebugPort == "" {
		return
	}
	
	slog.Debug("触发 WebUI 软性关闭 (DevTools Protocol)")
	apiURL := fmt.Sprintf("http://127.0.0.1:%s/json", safeDebugPort)
	if resp, err := safeGet(apiURL); err == nil {
		defer resp.Body.Close()
		var targets []map[string]interface{}
		if json.NewDecoder(resp.Body).Decode(&targets) == nil {
			for _, t := range targets {
				if id, ok := t["id"].(string); ok {
					if closeResp, closeErr := safeGet(fmt.Sprintf("http://127.0.0.1:%s/json/close/%s", safeDebugPort, id)); closeErr == nil {
						_ = closeResp.Body.Close()
					}
				}
			}
		}
	}

	time.Sleep(500 * time.Millisecond)
	pid := atomic.LoadUint32(&isolatedWebUIPid)
	if pid != 0 && sys.IsPidRunning(pid, "") {
		slog.Warn("正常关闭超时，强制结束浏览器进程", "PID", pid)
		sys.HardKill(pid)
	}
	atomic.StoreUint32(&isolatedWebUIPid, 0)
}
