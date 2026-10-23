package webui

import (
	"fmt"
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
	initCh := make(chan bool)

	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		winW, winH, _, _ := calculateWindowBounds()
		userDataDir := filepath.Join(cfg.BaseDir, domain.WebCacheDir, webview2Tag)
		_ = os.MkdirAll(userDataDir, 0755)


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

		// 1. 设置图标与暗黑标题栏（新窗口默认在前台，无需反复触发 TopMost 闪切）
		sys.SetWindowIconClean(hwnd, "APP")
		isDark := sys.IsSystemDarkMode()
		if isDark {
			sys.SetWindowDarkMode(hwnd, true)
		}

		// 2. 确定初始底色
		bg := "#F8F9FA"
		fg := "#212529"
		if isDark {
			bg = "#181818"
			fg = "#ECECEC"
		}

		// 3. 健壮的防白屏脚本：利用 MutationObserver 在 html 节点出现的第 0 毫秒打上 dark 类
		// 严密检查 null，彻底杜绝 TypeError 导致的脚本中断
		bgScript := fmt.Sprintf(`
			(function() {
				const bg = '%s';
				const fg = '%s';
				let isDark = %t;
				try {
					const local = localStorage.getItem('theme') || 
					              localStorage.getItem('theme-mode') || 
					              localStorage.getItem('meta-settings');
					if (local && local.includes('dark')) isDark = true;
					if (local && local.includes('light')) isDark = false;
				} catch(e) {}

				function inject() {
					const html = document.documentElement;
					if (html) {
						if (isDark && !html.classList.contains('dark')) {
							html.classList.add('dark');
						}
						html.style.backgroundColor = bg;
					}
					const target = document.head || document.documentElement;
					if (target && !document.getElementById('__tray_pre_bg__')) {
						const style = document.createElement('style');
						style.id = '__tray_pre_bg__';
						style.textContent = 'html, body, #app, #root { background-color: ' + bg + ' !important; color: ' + fg + ' !important; }';
						target.appendChild(style);
					}
				}

				inject();

				if (document.readyState === 'loading') {
					const observer = new MutationObserver(() => {
						inject();
						if (document.body) {
							observer.disconnect();
						}
					});
					observer.observe(document, { childList: true, subtree: true });
					document.addEventListener('DOMContentLoaded', inject);
				}
			})();
		`, bg, fg, isDark)
		w.Init(bgScript)

		initCh <- true

		defer func() {
			m.hwnd.Store(0)
			w.Destroy()
		}()

		w.Navigate(finalURL)
		emitEvent(eventCh, EventReady)

		w.Run()
	}()

	return <-initCh
}
