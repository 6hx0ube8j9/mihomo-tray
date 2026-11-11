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
	"sync"
	"sync/atomic"
	"time"

	"github.com/jchv/go-webview2"
	"github.com/jchv/go-webview2/webviewloader"

	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/sys"
)

const (
	appIconResourceID  = 1
	webviewInitTimeout = 15 * time.Second
)

var (
	wv2CheckOnce sync.Once
	wv2Available bool
)

func isWebView2Available() bool {
	wv2CheckOnce.Do(func() {
		v, err := webviewloader.GetInstalledVersion()
		wv2Available = (err == nil && v != "")
		if !wv2Available {
			slog.Debug("未检测到 WebView2 ，后续请求将直接降级")
		}
	})
	return wv2Available
}

type webviewAdapter struct {
	w    webview2.WebView
	hwnd uintptr
}

func (a *webviewAdapter) Destroy() {
	if a.hwnd != 0 && sys.IsWindowHung(a.hwnd) {
		slog.Warn("WebView2 窗口无响应，执行强制隐藏", "hwnd", a.hwnd)
		sys.HideWindow(a.hwnd)
	}

	if a.w != nil {
		a.w.Terminate()
	}
}

func (a *webviewAdapter) SetDarkMode(isDark bool) {
	if a.w != nil {
		a.w.Dispatch(func() {
			a.w.SetDarkMode(isDark)
		})
	}
}

func (m *Manager) launchWebView2(cfg Config, finalURL string, eventCh chan<- Event, onReady func()) bool {
	if !isWebView2Available() {
		return false
	}

	userDataDir := filepath.Join(cfg.BaseDir, domain.WebCacheDir, domain.WebOpenModeWebView2)
	if err := os.MkdirAll(userDataDir, 0755); err != nil {
		slog.Error("创建 WebView2 缓存目录失败", "path", userDataDir, "err", err)
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
			if w != nil {
				w.Destroy()
			}
		}()

		defer func() {
			if r := recover(); r != nil {
				slog.Error("WebView2 渲染线程异常崩溃", "err", r, "stack", string(debug.Stack()))
				if hwnd != 0 {
					sys.HideWindow(hwnd)
				}
				select {
				case initCh <- false:
				default:
				}
			}
		}()

		winW, winH := calculateWindowSize()

		var additionalArgs string
		if p := strings.TrimSpace(cfg.ProxyPort); p != "" && p != "0" {
			additionalArgs = fmt.Sprintf(
				"--proxy-server=%s --proxy-bypass-list=127.0.0.1;localhost;<local>",
				net.JoinHostPort("127.0.0.1", p),
			)
		}

		w = webview2.NewWithOptions(webview2.WebViewOptions{
			AutoFocus:                  false,
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

		m.wvMu.Lock()
		if aborted.Load() {
			m.wvMu.Unlock()
			sys.HideWindow(hwnd)
			return
		}
		m.hwnd.Store(hwnd)
		m.wv = &webviewAdapter{w: w, hwnd: hwnd}
		m.wvMu.Unlock()

		defer func() {
			m.hwnd.Store(0)
			m.wvMu.Lock()
			m.wv = nil
			m.wvMu.Unlock()
		}()

		w.SetNewWindowHandler(func(url string) bool {
			slog.Debug("阻止 WebView2 弹出新窗口", "url", url)
			return false
		})

		select {
		case initCh <- true:
		default:
		}

		w.Navigate(finalURL)
		emitEvent(eventCh, EventReady)

		sys.FocusWindowSilky(hwnd)

		if onReady != nil {
			go func() {
				time.Sleep(150 * time.Millisecond)
				onReady()
			}()
		}

		w.Run()
	}()

	select {
	case ok := <-initCh:
		if !ok {
			return false
		}
		return true

	case <-time.After(webviewInitTimeout):
		slog.Error("WebView2 启动超时，触发降级保护", "timeout", webviewInitTimeout)
		aborted.Store(true)

		if hwnd := m.hwnd.Load(); hwnd != 0 {
			sys.HideWindow(hwnd)
		}

		return false
	}
}
