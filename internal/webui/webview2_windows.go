package webui

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"github.com/jchv/go-webview2/pkg/edge"
	"golang.org/x/sys/windows"

	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/sys"
)

const (
	webview2Tag = "webview2"

	// Win32 窗口样式与消息常量
	csHRedraw           = 0x0002
	csVRedraw           = 0x0001
	wsOverlappedWindow  = 0x00CF0000
	idcArrow            = 32512

	wmDestroy           = 0x0002
	wmMove              = 0x0003
	wmSize              = 0x0005
	wmSetFocus          = 0x0007
	wmClose             = 0x0010
)

var (
	modUser32            = windows.NewLazySystemDLL("user32.dll")
	modGdi32             = windows.NewLazySystemDLL("gdi32.dll")
	modKernel32          = windows.NewLazySystemDLL("kernel32.dll")

	procRegisterClassExW = modUser32.NewProc("RegisterClassExW")
	procCreateWindowExW  = modUser32.NewProc("CreateWindowExW")
	procDefWindowProcW   = modUser32.NewProc("DefWindowProcW")
	procDestroyWindow    = modUser32.NewProc("DestroyWindow")
	procPostQuitMessage  = modUser32.NewProc("PostQuitMessage")
	procGetMessageW      = modUser32.NewProc("GetMessageW")
	procTranslateMessage = modUser32.NewProc("TranslateMessage")
	procDispatchMessageW = modUser32.NewProc("DispatchMessageW")
	procLoadCursorW      = modUser32.NewProc("LoadCursorW")

	procCreateSolidBrush = modGdi32.NewProc("CreateSolidBrush")
	procDeleteObject     = modGdi32.NewProc("DeleteObject")
)

type wndClassExW struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     windows.Handle
	hIcon         windows.Handle
	hCursor       windows.Handle
	hbrBackground windows.Handle
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       windows.Handle
}

type point struct {
	x, y int32
}

type winMsg struct {
	hwnd    windows.Handle
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      point
}

var (
	registerClassOnce sync.Once
	windowClassName   *uint16
	windowInstances   sync.Map 
)

var wndProcCallback = windows.NewCallback(func(hwnd uintptr, uMsg uint32, wParam, lParam uintptr) uintptr {
	val, exists := windowInstances.Load(hwnd)
	var chromium *edge.Chromium
	if exists {
		chromium = val.(*edge.Chromium)
	}

	switch uMsg {
	case wmSize:
		if chromium != nil {
			chromium.Resize()
		}
		return 0
	case wmMove:
		if chromium != nil {
			_ = chromium.NotifyParentWindowPositionChanged()
		}
		return 0
	case wmSetFocus:
		if chromium != nil {
			chromium.Focus()
		}
		return 0
	case wmClose:
		procDestroyWindow.Call(hwnd)
		return 0
	case wmDestroy:
		windowInstances.Delete(hwnd)
		procPostQuitMessage.Call(0)
		return 0
	}

	ret, _, _ := procDefWindowProcW.Call(hwnd, uintptr(uMsg), wParam, lParam)
	return ret
})

func (m *Manager) launchWebView2(cfg Config, finalURL string, eventCh chan<- Event) bool {
	initCh := make(chan bool)

	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		winW, winH, startX, startY := calculateWindowBounds()
		userDataDir := filepath.Join(cfg.BaseDir, domain.WebCacheDir, webview2Tag)
		_ = os.MkdirAll(userDataDir, 0755)

		sys.ClaimForegroundPrivilege()

		isDark := sys.IsSystemDarkMode()

		// 1. 注册深色底板窗口类（画刷直接使用 #181818 或 #F8F9FA）
		var hinstance windows.Handle
		_ = windows.GetModuleHandleEx(0, nil, &hinstance)

		registerClassOnce.Do(func() {
			windowClassName, _ = windows.UTF16PtrFromString("MihomoTrayHostWindow")
			cursor, _, _ := procLoadCursorW.Call(0, uintptr(idcArrow))

			// Windows COLORREF 为 0x00BBGGRR
			brushColor := uintptr(0x00181818) // #181818 (暗黑底色)
			if !isDark {
				brushColor = uintptr(0x00FAF9F8) // #F8F9FA (浅色底色)
			}
			hBrush, _, _ := procCreateSolidBrush.Call(brushColor)

			wc := wndClassExW{
				cbSize:        uint32(unsafe.Sizeof(wndClassExW{})),
				style:         csHRedraw | csVRedraw,
				lpfnWndProc:   wndProcCallback,
				hInstance:     hinstance,
				hCursor:       windows.Handle(cursor),
				hbrBackground: windows.Handle(hBrush),
				lpszClassName: windowClassName,
			}
			procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
		})

		windowTitle, _ := windows.UTF16PtrFromString(domain.AppName)
		hwndRet, _, _ := procCreateWindowExW.Call(
			0,
			uintptr(unsafe.Pointer(windowClassName)),
			uintptr(unsafe.Pointer(windowTitle)),
			wsOverlappedWindow,
			uintptr(startX),
			uintptr(startY),
			uintptr(winW),
			uintptr(winH),
			0,
			0,
			uintptr(hinstance),
			0,
		)

		hwnd := hwndRet
		if hwnd == 0 {
			slog.Error("创建原生宿主窗口失败")
			initCh <- false
			return
		}

		m.hwnd.Store(hwnd)

		sys.SetWindowIconClean(hwnd, "APP")
		if isDark {
			sys.SetWindowDarkMode(hwnd, true)
		}

		chromium := edge.NewChromium()
		chromium.DataPath = userDataDir
		chromium.SetPermission(edge.CoreWebView2PermissionKindClipboardRead, edge.CoreWebView2PermissionStateAllow)

		windowInstances.Store(hwnd, chromium)

		if !chromium.Embed(hwnd) {
			slog.Info("未检测到可用的 WebView2 运行时，准备降级到 Tier 2 (--app 模式)")
			windowInstances.Delete(hwnd)
			procDestroyWindow.Call(hwnd)
			initCh <- false
			return
		}
		chromium.Resize()

		if settings, err := chromium.GetSettings(); err == nil {
			_ = settings.PutAreDefaultContextMenusEnabled(cfg.Debug)
		}

		bg := "#F8F9FA"
		fg := "#212529"
		if isDark {
			bg = "#181818"
			fg = "#ECECEC"
		}

		preloadScript := fmt.Sprintf(`
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

				function applyStyle() {
					const html = document.documentElement;
					if (html) {
						if (isDark && !html.classList.contains('dark')) {
							html.classList.add('dark');
						}
						html.style.backgroundColor = bg;
					}
					const target = document.head || document.documentElement;
					if (target && !document.getElementById('__preload_guard_bg__')) {
						const style = document.createElement('style');
						style.id = '__preload_guard_bg__';
						style.textContent = 'html, body, #app, #root { background-color: ' + bg + ' !important; color: ' + fg + ' !important; }';
						target.appendChild(style);
					}
				}

				applyStyle();

				function notifyReady() {
					if (window.chrome && window.chrome.webview) {
						window.chrome.webview.postMessage('ui:ready');
					}
				}

				if (document.readyState === 'complete' || document.readyState === 'interactive') {
					notifyReady();
				} else {
					document.addEventListener('DOMContentLoaded', notifyReady);
				}
			})();
		`, bg, fg, isDark)
		chromium.Init(preloadScript)

		var showOnce sync.Once
		presentWindow := func() {
			showOnce.Do(func() {
				sys.FocusWindowSilky(hwnd)
				emitEvent(eventCh, EventReady)
			})
		}

		chromium.MessageCallback = func(msg string) {
			if msg == "ui:ready" {
				presentWindow()
			}
		}

		initCh <- true

		defer func() {
			m.hwnd.Store(0)
			windowInstances.Delete(hwnd)
		}()

		time.AfterFunc(250*time.Millisecond, func() {
			presentWindow()
		})

		chromium.Navigate(finalURL)

		var msg winMsg
		for {
			r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
			if int32(r) <= 0 {
				break
			}
			procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
			procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
		}
	}()

	return <-initCh
}
