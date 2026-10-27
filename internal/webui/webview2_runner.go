package webui

import (
	"log/slog"
	"path/filepath"
	"runtime"
	"runtime/debug"

	"github.com/jchv/go-webview2"
	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/sys"
)

type webviewAdapter struct {
	w webview2.WebView
}

func (a *webviewAdapter) Destroy() {
	if a.w != nil {
		a.w.Destroy()
	}
}

func (a *webviewAdapter) SetDarkMode(isDark bool) {
	if a.w != nil {
		a.w.Dispatch(func() {
			a.w.SetDarkMode(isDark)
		})
	}
}

func (m *Manager) launchWebView2(cfg Config, finalURL string, eventCh chan<- Event) bool {
	initCh := make(chan bool, 1)

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
		userDataDir := filepath.Join(cfg.BaseDir, domain.WebCacheDir, domain.WebOpenModeWebView2)
		isDark := cfg.SystemTheme == sys.ThemeDark

		w := webview2.NewWithOptions(webview2.WebViewOptions{
			AutoFocus: true,
			DataPath:  userDataDir,
			WindowOptions: webview2.WindowOptions{
				Title:    domain.AppName,
				Width:    uint(winW),
				Height:   uint(winH),
				Center:   true,
				DarkMode: isDark,
			},
		})
		if w == nil {
			initCh <- false
			return
		}

		hwnd := uintptr(w.Window())
		m.hwnd.Store(hwnd)

		m.wvMu.Lock()
		m.wv = &webviewAdapter{w: w}
		m.wvMu.Unlock()

		defer func() {
			m.hwnd.Store(uintptr(0))
			m.wvMu.Lock()
			m.wv = nil
			m.wvMu.Unlock()
		}()

		w.SetNewWindowHandler(func(url string) bool {
			slog.Debug("拦截网页新窗口跳转请求", "url", url)
			return false
		})

		sys.SetWindowIconClean(hwnd, "APP")

		initCh <- true
		w.Navigate(finalURL)
		sys.FocusWindowSilky(hwnd)
		emitEvent(eventCh, EventReady)

		w.Run()
	}()

	return <-initCh
}
