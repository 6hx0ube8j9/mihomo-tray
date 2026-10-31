//go:build windows

package webui

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jchv/go-webview2"
	"github.com/jchv/go-webview2/webviewloader"
	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/sys"
)

const (
	appIconResourceID  = 1
	webviewInitTimeout = 5 * time.Second
)

type webviewAdapter struct {
	w    webview2.WebView
	hwnd uintptr
}

func (a *webviewAdapter) Destroy() {
	if a.hwnd != 0 {
		if sys.IsWindowHung(a.hwnd) {
			slog.Warn("检测到 WebView2 窗口已卡死，强制隐藏以破除前台焦点锁定", "hwnd", a.hwnd)
			sys.HideWindow(a.hwnd)
		}
	}
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

func isWebView2Available() bool {
	v, err := webviewloader.GetInstalledVersion()
	return err == nil && v != ""
}

func (m *Manager) launchWebView2(cfg Config, finalURL string, eventCh chan<- Event) bool {
	if !isWebView2Available() {
		slog.Info("未检测到 WebView2 运行时")
		return false
	}

	userDataDir := filepath.Join(cfg.BaseDir, domain.WebCacheDir, domain.WebOpenModeWebView2)
	if err := os.MkdirAll(userDataDir, 0755); err != nil {
		slog.Error("创建缓存目录失败", "path", userDataDir, "err", err)
		return false
	}

	initCh := make(chan bool, 1)
	var aborted atomic.Bool

	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		var w webview2.WebView
		var hwnd uintptr

		defer func() {
			if r := recover(); r != nil {
				slog.Error("WebView2 运行异常", "err", r, "stack", string(debug.Stack()))
				if hwnd != 0 {
					sys.HideWindow(hwnd)
				}
				if w != nil {
					w.Destroy()
				}
				select {
				case initCh <- false:
				default:
				}
			}
		}()

		winW, winH, _, _ := calculateWindowBounds()

		var additionalArgs string
		if p := strings.TrimSpace(cfg.ProxyPort); p != "" && p != "0" {
			additionalArgs = fmt.Sprintf(
				"--proxy-server=%s --proxy-bypass-list=127.0.0.1;localhost;<local>",
				net.JoinHostPort("127.0.0.1", p),
			)
		}

		w = webview2.NewWithOptions(webview2.WebViewOptions{
			AutoFocus:                  true,
			DataPath:                   userDataDir,
			AdditionalBrowserArguments: additionalArgs,
			WindowOptions: webview2.WindowOptions{
				Title:    domain.AppName,
				Width:    uint(winW),
				Height:   uint(winH),
				IconId:   appIconResourceID,
				Center:   true,
				DarkMode: cfg.IsDark,
			},
		})

		if w == nil {
			select {
			case initCh <- false:
			default:
			}
			return
		}

		hwnd = uintptr(w.Window())

		if aborted.Load() {
			slog.Warn("WebView2 初始化已超时，终止窗口加载并回收资源")
			sys.HideWindow(hwnd)
			w.Destroy()
			return
		}

		m.hwnd.Store(hwnd)

		m.wvMu.Lock()
		m.wv = &webviewAdapter{w: w, hwnd: hwnd}
		m.wvMu.Unlock()

		defer func() {
			m.hwnd.Store(0)
			m.wvMu.Lock()
			m.wv = nil
			m.wvMu.Unlock()
		}()

		w.SetNewWindowHandler(func(url string) bool {
			slog.Debug("已阻止在新窗口中打开链接", "url", url)
			return false
		})

		select {
		case initCh <- true:
		default:
		}

		w.Navigate(finalURL)
		emitEvent(eventCh, EventReady)

		w.Run()
	}()

	select {
	case ok := <-initCh:
		return ok
	case <-time.After(webviewInitTimeout):
		slog.Error("WebView2 启动超时，准备降级到独立浏览器")
		aborted.Store(true)

		if hwnd := m.hwnd.Swap(0); hwnd != 0 {
			sys.HideWindow(hwnd)
		}

		m.wvMu.Lock()
		if m.wv != nil {
			m.wv.Destroy()
			m.wv = nil
		}
		m.wvMu.Unlock()

		return false
	}
}
