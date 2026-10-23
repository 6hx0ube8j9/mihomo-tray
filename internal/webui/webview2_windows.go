package webui

import (
	"log/slog"
	"os"
	"path/filepath"
	"runtime"

	"github.com/jchv/go-webview2"
	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/sys"
)

const webview2Tag = "webview2"

func (m *Manager) launchWebView2(cfg Config, finalURL string, eventCh chan<- Event) bool {
	winW, winH, _, _ := calculateWindowBounds()
	userDataDir := filepath.Join(cfg.BaseDir, domain.WebCacheDir, webview2Tag)
	_ = os.MkdirAll(userDataDir, 0755)

	w := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug:     false,
		AutoFocus: true,
		DataPath:  userDataDir,
		WindowOptions: webview2.WindowOptions{
			Title:  "Dashboard",
			Width:  uint(winW),
			Height: uint(winH),
			Center: true,
		},
	})

	if w == nil {
		slog.Info("未检测到可用的 WebView2 运行时组件，准备降级到 Tier 2 (--app 模式)")
		return false
	}

	hwnd := uintptr(w.Window())
	m.hwnd.Store(hwnd)

	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer func() {
			m.hwnd.Store(0)
			w.Destroy()
		}()

		w.Navigate(finalURL)
		sys.FocusWindowSilky(hwnd)
		emitEvent(eventCh, EventReady)

		w.Run()
	}()

	return true
}
