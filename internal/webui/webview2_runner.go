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

	"github.com/jchv/go-webview2"
	"github.com/jchv/go-webview2/webviewloader"
	"mihomo-tray/internal/domain"
)

const appIconResourceID = 1

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

	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		var w webview2.WebView
		defer func() {
			if r := recover(); r != nil {
				slog.Error("WebView2 运行异常", "err", r, "stack", string(debug.Stack()))
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
			initCh <- false
			return
		}

		hwnd := uintptr(w.Window())
		m.hwnd.Store(hwnd)

		m.wvMu.Lock()
		m.wv = &webviewAdapter{w: w}
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

		initCh <- true
		w.Navigate(finalURL)
		emitEvent(eventCh, EventReady)

		w.Run()
	}()

	return <-initCh
}
