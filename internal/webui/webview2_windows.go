package webui

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"

	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/sys"
)

const (
	webview2Tag = "webview2"
	swHide      = 0 // Win32 SW_HIDE
)

var (
	modUser32      = windows.NewLazySystemDLL("user32.dll")
	procShowWindow = modUser32.NewProc("ShowWindow")
)

func (m *Manager) launchWebView2(cfg Config, finalURL string, eventCh chan<- Event) bool {
	initCh := make(chan bool)

	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		winW, winH, _, _ := calculateWindowBounds()
		userDataDir := filepath.Join(cfg.BaseDir, domain.WebCacheDir, webview2Tag)
		_ = os.MkdirAll(userDataDir, 0755)

		sys.ClaimForegroundPrivilege()

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
			slog.Info("未检测到可用的 WebView2 运行时，准备降级到 Tier 2 (--app 模式)")
			initCh <- false
			return
		}

		hwnd := uintptr(w.Window())
		m.hwnd.Store(hwnd)

		// 1. 核心关键：窗口刚创建时强制静默隐藏，严禁在加载期间暴露给用户
		procShowWindow.Call(hwnd, swHide)

		// 2. 准备图标与深色模式标题栏
		sys.SetWindowIconClean(hwnd, "APP")
		isDark := sys.IsSystemDarkMode()
		if isDark {
			sys.SetWindowDarkMode(hwnd, true)
		}

		// 3. 注入首帧防白脚本：在 HTML 解析的第一时间拦截，提前将 html 标签与 app 容器打上暗黑底色
		// 彻底解决 Vue/React 在 JS 执行前渲染 Tailwind 白色默认底的问题
		bgScript := fmt.Sprintf(`
			(function() {
				let isDark = %t;
				try {
					const local = localStorage.getItem('theme') || 
					              localStorage.getItem('theme-mode') || 
					              localStorage.getItem('meta-settings');
					if (local && local.includes('dark')) isDark = true;
					if (local && local.includes('light')) isDark = false;
				} catch(e) {}

				const bg = isDark ? '#181818' : '#F8F9FA';
				const fg = isDark ? '#ECECEC' : '#212529';

				if (isDark) {
					document.documentElement.classList.add('dark');
				}
				document.documentElement.style.backgroundColor = bg;

				const style = document.createElement('style');
				style.id = '__preload_dark_bg__';
				style.innerHTML = 'html, body, #app { background-color: ' + bg + ' !important; color: ' + fg + '; }';
				const target = document.head || document.documentElement;
				if (target) {
					target.appendChild(style);
				}

				// DOM 解析就绪后，主动通知 Go 宿主拉起窗口
				function notify() {
					if (window.__panelReady__) {
						window.__panelReady__();
					}
				}

				if (document.readyState === 'complete' || document.readyState === 'interactive') {
					notify();
				} else {
					document.addEventListener('DOMContentLoaded', notify);
				}
			})();
		`, isDark)
		w.Init(bgScript)

		// 4. 展示窗口回调（确保仅执行一次）
		var showOnce sync.Once
		showWindow := func() {
			showOnce.Do(func() {
				// 此时页面 DOM 与基础底色已就绪，瞬间置顶弹出，零白屏
				sys.FocusWindowSilky(hwnd)
				emitEvent(eventCh, EventReady)
			})
		}

		// 绑定前端就绪事件通知
		_ = w.Bind("__panelReady__", func() {
			showWindow()
		})

		initCh <- true

		defer func() {
			m.hwnd.Store(0)
			w.Destroy()
		}()

		// 5. 超时保底：本地 127.0.0.1 加载一般仅需 20~50ms
		// 设定 300ms 超时强制展示，防止极端前端报错导致窗口永久隐藏
		time.AfterFunc(300*time.Millisecond, func() {
			showWindow()
		})

		// 6. 在后台启动导航，完全不阻塞主消息循环
		w.Navigate(finalURL)
		w.Run()
	}()

	return <-initCh
}
