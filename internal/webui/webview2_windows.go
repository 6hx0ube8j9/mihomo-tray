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

		// 核心保护：捕获协程内的一切致命 Panic 并输出具体行号
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

		slog.Info("开始创建 WebView2 实例...")
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
		slog.Info("WebView2 实例创建成功")

		hwnd := uintptr(w.Window())
		// 严防类型不一致：统一使用 uintptr(0)
		m.hwnd.Store(hwnd)

		// 容错执行外观调整
		func() {
			defer func() { _ = recover() }()
			sys.SetWindowIconClean(hwnd, "APP")
			if sys.IsSystemDarkMode() {
				sys.SetWindowDarkMode(hwnd, true)
			}
		}()

		initCh <- true

		defer func() {
			m.hwnd.Store(uintptr(0))
			w.Destroy()
		}()

		slog.Info("正在发起页面导航", "url", finalURL)
		w.Navigate(finalURL)

		func() {
			defer func() { _ = recover() }()
			sys.FocusWindowSilky(hwnd)
		}()

		emitEvent(eventCh, EventReady)

		slog.Info("进入 WebView2 消息循环")
		w.Run()
		slog.Info("WebView2 消息循环退出")
	}()

	return <-initCh
}
