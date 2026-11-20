//go:build windows

package webui

import (
	"log/slog"
	"os"
	"path/filepath"
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
	webviewInitTimeout = 12 * time.Second
)

var (
	wv2CheckOnce    sync.Once
	wv2Available    bool
	wv2Broken       atomic.Bool
	wv2CacheCleaned atomic.Bool
)

func isCorruptCacheError(err error) bool {
	code, ok := webview2.HRESULTCode(err)
	if !ok {
		return false
	}
	switch code {
	case 0x8007139F, // ERROR_INVALID_STATE
		0x80070570, // ERROR_FILE_CORRUPT
		0x80070017: // ERROR_CRC
		return true
	default:
		return false
	}
}

func cleanCorruptWebViewCache(userDataDir string, initErr error) {
	if !isCorruptCacheError(initErr) {
		return
	}
	if !wv2CacheCleaned.CompareAndSwap(false, true) {
		return
	}

	cleanDir := filepath.Clean(userDataDir)
	if cleanDir == "" || cleanDir == "." || filepath.Dir(cleanDir) == cleanDir || filepath.Base(cleanDir) != domain.WebViewCacheDirName {
		return
	}

	slog.Warn("清理损坏的 WebView2 缓存", "path", cleanDir)
	_ = os.RemoveAll(cleanDir)
}

func isWebView2Available() bool {
	wv2CheckOnce.Do(func() {
		v, err := webviewloader.GetInstalledVersion()
		wv2Available = (err == nil && v != "")
		if !wv2Available {
			slog.Debug("未检测到 WebView2 运行时")
		}
	})
	return wv2Available
}

type webviewAdapter struct {
	w         webview2.WebView
	hwnd      uintptr
	destroyed atomic.Bool
}

func (a *webviewAdapter) Destroy() {
	if !a.destroyed.CompareAndSwap(false, true) {
		return
	}
	if a.hwnd != 0 && sys.IsWindowHung(a.hwnd) {
		slog.Warn("WebView2 窗口无响应", "hwnd", a.hwnd)
		sys.HideWindow(a.hwnd)
	}
	if a.w != nil {
		a.w.Destroy()
	}
}

func (a *webviewAdapter) SetDarkMode(isDark bool) {
	if !a.destroyed.Load() && a.w != nil {
		a.w.SetDarkMode(isDark)
	}
}

func (m *Manager) cleanupWebView2() {
	m.wvMu.Lock()
	if m.wv != nil {
		m.wv.Destroy()
		m.wv = nil
	}
	m.wvMu.Unlock()

	m.hwnd.Store(0)
}

func (m *Manager) launchWebView2(cfg Config, cacheRoot, finalURL string, eventCh chan<- Event) bool {
	if !isWebView2Available() {
		return false
	}
	if cfg.WebOpenMode != domain.WebOpenModeWebView && wv2Broken.Load() {
		return false
	}

	userDataDir := filepath.Join(cacheRoot, domain.WebViewCacheDirName)
	if err := os.MkdirAll(userDataDir, 0755); err != nil {
		slog.Warn("创建 WebView2 缓存目录失败", "path", userDataDir, "err", err)
		return false
	}

	initCh := make(chan bool, 1)
	var aborted atomic.Bool

	go func() {
		var adapter *webviewAdapter
		var hwnd uintptr

		defer func() {
			if adapter != nil {
				adapter.Destroy()
			}
		}()

		defer func() {
			if r := recover(); r != nil {
				slog.Error("WebView2 运行崩溃", "err", r, "stack", string(debug.Stack()))
				wv2Broken.Store(true)
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
		additionalArgs := strings.Join(buildProxyArgs(cfg.ProxyPort), " ")

		w, err := webview2.NewWithOptionsErr(webview2.WebViewOptions{
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
		if err != nil {
			slog.Warn("WebView2 初始化失败", "err", err)
			wv2Broken.Store(true)
			cleanCorruptWebViewCache(userDataDir, err)
			select {
			case initCh <- false:
			default:
			}
			return
		}

		hwnd = uintptr(w.Window())
		adapter = &webviewAdapter{w: w, hwnd: hwnd}

		m.wvMu.Lock()
		if aborted.Load() {
			m.wvMu.Unlock()
			sys.HideWindow(hwnd)
			adapter.Destroy()
			w.Run()
			return
		}
		oldWV := m.wv
		m.hwnd.Store(hwnd)
		m.wv = adapter
		wv2Broken.Store(false)
		m.wvMu.Unlock()

		if oldWV != nil {
			oldWV.Destroy()
		}

		defer func() {
			m.hwnd.CompareAndSwap(hwnd, 0)
			m.wvMu.Lock()
			if m.wv == adapter {
				m.wv = nil
			}
			m.wvMu.Unlock()
		}()

		w.SetProcessFailedHandler(func(ev webview2.ProcessFailedEvent) {
			if ev.IsFatal {
				slog.Error("WebView2 进程异常退出", "err", ev.Err())
				adapter.Destroy()
				return
			}
			slog.Warn("WebView2 子进程异常", "err", ev.Err())
		})

		w.Navigate(finalURL)
		emitEvent(eventCh, EventReady)

		select {
		case initCh <- true:
		default:
		}

		w.Run()
		adapter.destroyed.Store(true)
	}()

	select {
	case ok := <-initCh:
		return ok

	case <-time.After(webviewInitTimeout):
		slog.Warn("WebView2 启动超时", "timeout", webviewInitTimeout)

		m.wvMu.Lock()
		aborted.Store(true)
		wv2Broken.Store(true)
		staleWV := m.wv
		m.wv = nil
		m.wvMu.Unlock()

		if staleWV != nil {
			staleWV.Destroy()
		}
		if hwnd := m.hwnd.Swap(0); hwnd != 0 {
			sys.HideWindow(hwnd)
		}

		return false
	}
}