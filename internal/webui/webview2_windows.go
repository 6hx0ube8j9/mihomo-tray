package webui

import (
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"

	"github.com/jchv/go-webview2"
	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/sys"
)

const webview2Tag = "webview2"

func (m *Manager) launchWebView2(cfg Config, finalURL string, eventCh chan<- Event) bool {
	initCh := make(chan bool)

	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		defer func() {
			if r := recover(); r != nil {
				slog.Error("WebView2 协程崩溃", "panic", r, "stack", string(debug.Stack()))
				select {
				case initCh <- false:
				default:
				}
			}
		}()

		winW, winH, _, _ := calculateWindowBounds()
		if winW <= 0 || winH <= 0 {
			winW, winH = 1024, 720
		}

		userDataDir := filepath.Join(cfg.BaseDir, domain.WebCacheDir, webview2Tag)
		_ = os.MkdirAll(userDataDir, 0755)

		w := webview2.NewWithOptions(webview2.WebViewOptions{
			Debug:     false,
			AutoFocus: true,
			DataPath:  userDataDir,
			WindowOptions: webview2.WindowOptions{
				Title:  domain.AppName,
				Width:  uint(winW),
				Height: uint(winH),
				Center: true,
			},
		})

		if w == nil {
			slog.Warn("未检测到可用的 WebView2 运行时，准备降级到 Tier 2")
			initCh <- false
			return
		}

		hwnd := uintptr(w.Window())
		m.hwnd.Store(hwnd)

		func() {
			defer func() { _ = recover() }()
			sys.SetWindowIconClean(hwnd, "APP")
			if sys.IsSystemDarkMode() {
				sys.SetWindowDarkMode(hwnd, true)
			}
		}()

		// ================= 核心：接管外链跳转 =================
		// 1. 注册 Go 回调，调用系统默认浏览器打开 URL
		_ = w.Bind("openExternal", func(targetURL string) {
			targetURL = strings.TrimSpace(targetURL)
			if targetURL == "" {
				return
			}

			// 安全校验：仅允许 http/https/file 协议
			parsed, err := url.Parse(targetURL)
			if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
				return
			}

			// Windows 原生呼起系统默认浏览器（无黑框、非阻塞）
			_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", targetURL).Start()
		})

		// 2. 注入全局前置 JS，捕获所有新窗口/外链行为
		const externalLinkScript = `
		(function() {
			// 拦截全局 window.open 脚本调用
			window.open = function(url) {
				if (url && typeof url === 'string' && url.trim() !== '') {
					window.openExternal(url);
				}
				return null;
			};

			// 全局捕获阶段代理所有 <a> 标签点击
			document.addEventListener('click', function(e) {
				const a = e.target.closest('a');
				if (!a || !a.href) return;

				const href = a.href.trim();
				// 跳过非 HTTP 锚点与伪协议
				if (!href.startsWith('http://') && !href.startsWith('https://')) return;

				const isBlank = a.target === '_blank';
				const isExternalHost = a.hostname && a.hostname !== window.location.hostname;

				// 如果指定了新窗口打开，或者跳转到非当前控制面板域名（如 GitHub、文档站）
				if (isBlank || isExternalHost) {
					e.preventDefault();
					e.stopPropagation();
					window.openExternal(href);
				}
			}, true);
		})();
		`
		w.Init(externalLinkScript)
		// ====================================================

		initCh <- true

		defer func() {
			m.hwnd.Store(uintptr(0))
			w.Destroy()
		}()

		w.Navigate(finalURL)

		func() {
			defer func() { _ = recover() }()
			sys.FocusWindowSilky(hwnd)
		}()

		emitEvent(eventCh, EventReady)

		w.Run()
	}()

	return <-initCh
}
