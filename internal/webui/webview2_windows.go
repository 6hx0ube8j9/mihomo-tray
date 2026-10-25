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

const (
	webview2Tag = "webview2"

	externalLinkScript = `
	(function() {
		function openUrl(href) {
			if (!href || typeof href !== 'string') return;
			href = href.trim();
			if (!href.startsWith('http://') && !href.startsWith('https://')) return;
			if (window.openExternal) {
				window.openExternal(href);
			}
		}

		window.open = function(url) {
			if (url) openUrl(url);
			return null;
		};

		document.addEventListener('click', function(e) {
			let el = e.target;
			while (el && el !== document) {
				if (el.tagName && el.tagName.toLowerCase() === 'a') {
					let href = el.getAttribute('href') || el.href;
					if (typeof href === 'object' && href.baseVal) {
						href = href.baseVal;
					}
					if (typeof href === 'string') {
						href = href.trim();
						if (href.startsWith('http://') || href.startsWith('https://')) {
							if (el.target === '_blank' || (el.hostname && el.hostname !== window.location.hostname)) {
								e.preventDefault();
								e.stopPropagation();
								openUrl(href);
								return;
							}
						}
					}
				}
				el = el.parentElement;
			}
		}, true);
	})();`
)

func (m *Manager) launchWebView2(cfg Config, finalURL string, eventCh chan<- Event) bool {
	initCh := make(chan bool)

	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		defer func() {
			if r := recover(); r != nil {
				slog.Error("WebView2 运行时异常", "panic", r, "stack", string(debug.Stack()))
				select {
				case initCh <- false:
				default:
				}
			}
		}()

		winW, winH, _, _ := calculateWindowBounds()
		userDataDir := filepath.Join(cfg.BaseDir, domain.WebCacheDir, webview2Tag)
		_ = os.MkdirAll(userDataDir, 0755)

		w := webview2.NewWithOptions(webview2.WebViewOptions{
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
			initCh <- false
			return
		}

		hwnd := uintptr(w.Window())
		m.hwnd.Store(hwnd)
		defer func() {
			m.hwnd.Store(uintptr(0))
			w.Destroy()
		}()

		sys.SetWindowIconClean(hwnd, "APP")
		if sys.IsSystemDarkMode() {
			sys.SetWindowDarkMode(hwnd, true)
		}

		// 绑定外链桥接
		m.bindExternalLinkHandler(w)

		initCh <- true
		w.Navigate(finalURL)
		sys.FocusWindowSilky(hwnd)
		emitEvent(eventCh, EventReady)

		w.Run()
	}()

	return <-initCh
}

func (m *Manager) bindExternalLinkHandler(w webview2.WebView) {
	_ = w.Bind("openExternal", func(target string) {
		target = strings.TrimSpace(target)
		u, err := url.Parse(target)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return
		}
		slog.Info("收到外链跳转请求", "url", target)
		_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", target).Start()
	})
	w.Init(externalLinkScript)
}
