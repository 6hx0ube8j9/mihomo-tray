package webui

import (
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"

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

		// 页面导航与展示（外链已被底层 COM 原生全自动接管，不再需要任何 JS）
		initCh <- true
		w.Navigate(finalURL)
		sys.FocusWindowSilky(hwnd)
		emitEvent(eventCh, EventReady)

		w.Run()
	}()

	return <-initCh
}
