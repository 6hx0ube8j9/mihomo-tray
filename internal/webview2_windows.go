package webui

import (
	"log/slog"
	"os"
	"path/filepath"
	"runtime"

	"github.com/jchv/go-webview2"
	"golang.org/x/sys/windows/registry"
	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/sys"
)

const webview2Tag = "webview2"

func HasWebView2Runtime() bool {
	keys := []struct {
		root registry.Key
		path string
	}{
		{registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-6E3A3A7E3E58}`},
		{registry.CURRENT_USER, `Software\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-6E3A3A7E3E58}`},
	}
	for _, k := range keys {
		key, err := registry.OpenKey(k.root, k.path, registry.QUERY_VALUE)
		if err == nil {
			var ver string
			ver, _, err = key.GetStringValue("pv")
			_ = key.Close()
			if err == nil && ver != "" && ver != "0.0.0.0" {
				return true
			}
		}
	}
	return false
}

func (m *Manager) launchWebView2(cfg Config, finalURL string, eventCh chan<- Event) bool {
	if !HasWebView2Runtime() {
		slog.Debug("未检测到 WebView2 运行时，准备降级到 Tier 2 (--app 模式)")
		return false
	}

	userDataDir := filepath.Join(cfg.BaseDir, domain.WebCacheDir, webview2Tag)
	if err := os.MkdirAll(userDataDir, 0755); err != nil {
		slog.Warn("创建 WebView2 用户缓存目录失败", "path", userDataDir, "err", err)
	}

	winW, winH, _, _ := calculateWindowBounds()

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
		slog.Warn("WebView2 初始化创建失败，降级到 Tier 2")
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
