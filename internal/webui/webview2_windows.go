package webui

import (
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"syscall"

	"github.com/jchv/go-webview2"
	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/sys"
)

const (
	webview2Tag = "webview2"
	swShow      = 5 // Win32 SW_SHOW 常量
)

var (
	modUser32      = syscall.NewLazyDLL("user32.dll")
	procShowWindow = modUser32.NewProc("ShowWindow")
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
				Hidden: true, // 保持后台静默装配
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

		// 1. 在不可见状态下完成 Win32 样式与深色标题栏设置
		sys.SetWindowIconClean(hwnd, "APP")
		if sys.IsSystemDarkMode() {
			sys.SetWindowDarkMode(hwnd, true)
		}

		// 2. 页面导航
		initCh <- true
		w.Navigate(finalURL)

		// 3. 关键修复：先将隐藏窗口正式显示到屏幕，再执行平滑聚焦
		_, _, _ = procShowWindow.Call(hwnd, uintptr(swShow))
		sys.FocusWindowSilky(hwnd)

		emitEvent(eventCh, EventReady)

		w.Run()
	}()

	return <-initCh
}
