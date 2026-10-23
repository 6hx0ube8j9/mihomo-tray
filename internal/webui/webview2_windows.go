package webui

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/jchv/go-webview2/pkg/edge"
	"golang.org/x/sys/windows"

	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/sys"
)

const (
	webview2Tag = "webview2"

	// Win32 常量
	wsOverlappedWindow = 0x00CF0000
	wsClipChildren     = 0x02000000
	swHide             = 0
	swShow             = 5
	wmSize             = 0x0005
	wmDestroy          = 0x0002
	wmClose            = 0x0010
)

var (
	modUser32                = windows.NewLazySystemDLL("user32.dll")
	modGdi32                 = windows.NewLazySystemDLL("gdi32.dll")
	procRegisterClassExW     = modUser32.NewProc("RegisterClassExW")
	procCreateWindowExW      = modUser32.NewProc("CreateWindowExW")
	procDefWindowProcW       = modUser32.NewProc("DefWindowProcW")
	procPostQuitMessage      = modUser32.NewProc("PostQuitMessage")
	procGetMessageW          = modUser32.NewProc("GetMessageW")
	procTranslateMessage     = modUser32.NewProc("TranslateMessage")
	procDispatchMessageW     = modUser32.NewProc("DispatchMessageW")
	procPostMessageW         = modUser32.NewProc("PostMessageW")
	procCreateSolidBrush     = modGdi32.NewProc("CreateSolidBrush")

	wndClassOnce sync.Once
	wndClassName = "MihomoWebUIPanel"
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

type msg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      struct{ x, y int32 }
}

var (
	globalChromiumMap sync.Map
)

func wndProc(hwnd uintptr, message uint32, wParam, lParam uintptr) uintptr {
	switch message {
	case wmSize:
		if val, ok := globalChromiumMap.Load(hwnd); ok {
			c := val.(*edge.Chromium)
			c.Resize()
		}
		return 0
	case wmDestroy:
		globalChromiumMap.Delete(hwnd)
		procPostQuitMessage.Call(0)
		return 0
	}
	ret, _, _ := procDefWindowProcW.Call(hwnd, uintptr(message), wParam, lParam)
	return ret
}

func registerPanelClass(hInst windows.Handle, isDark bool) {
	wndClassOnce.Do(func() {
		// 核心防白机制 1：Win32 窗口类使用深色画刷填充背景，杜绝系统默认的白色画刷 (COLOR_WINDOW)
		var bgBrush uintptr
		if isDark {
			bgBrush, _, _ = procCreateSolidBrush.Call(0x00181818) // RGB(24, 24, 24)
		} else {
			bgBrush, _, _ = procCreateSolidBrush.Call(0x00FAF9F8)
		}

		clsNamePtr, _ := windows.UTF16PtrFromString(wndClassName)
		wc := wndClassExW{
			cbSize:        uint32(unsafe.Sizeof(wndClassExW{})),
			lpfnWndProc:   windows.NewCallback(wndProc),
			hInstance:     hInst,
			hbrBackground: windows.Handle(bgBrush),
			lpszClassName: clsNamePtr,
		}
		procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	})
}

func (m *Manager) launchWebView2(cfg Config, finalURL string, eventCh chan<- Event) bool {
	initCh := make(chan bool)

	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		winW, winH, startX, startY := calculateWindowBounds()
		userDataDir := filepath.Join(cfg.BaseDir, domain.WebCacheDir, webview2Tag)
		_ = os.MkdirAll(userDataDir, 0755)

		// 修正 1：调用 sys 中正确的函数名 GrantForegroundPrivilege
		sys.GrantForegroundPrivilege()[cite: 5]

		var hInst windows.Handle
		_ = windows.GetModuleHandleEx(0, nil, &hInst)

		isDark := sys.IsSystemDarkMode()
		registerPanelClass(hInst, isDark)

		clsNamePtr, _ := windows.UTF16PtrFromString(wndClassName)
		titlePtr, _ := windows.UTF16PtrFromString(domain.AppName)

		// 核心防白机制 2：创建窗口时坚决不加 WS_VISIBLE，初始在内存中完全静默
		hwndRet, _, _ := procCreateWindowExW.Call(
			0,
			uintptr(unsafe.Pointer(clsNamePtr)),
			uintptr(unsafe.Pointer(titlePtr)),
			wsOverlappedWindow|wsClipChildren, // 仅含基础样式与子控件裁剪，无 WS_VISIBLE
			uintptr(startX),
			uintptr(startY),
			uintptr(winW),
			uintptr(winH),
			0,
			0,
			uintptr(hInst),
			0,
		)
		hwnd := hwndRet
		if hwnd == 0 {
			initCh <- false
			return
		}

		m.hwnd.Store(hwnd)
		sys.SetWindowIconClean(hwnd, "APP")
		if isDark {
			sys.SetWindowDarkMode(hwnd, true)
		}

		// 初始化 Chromium 底层实例
		chromium := edge.NewChromium()
		chromium.DataPath = userDataDir
		chromium.SetPermission(edge.CoreWebView2PermissionKindClipboardRead, edge.CoreWebView2PermissionStateAllow)

		globalChromiumMap.Store(hwnd, chromium)

		// 将 WebView2 嵌入不可见的深色原生窗口
		if !chromium.Embed(hwnd) {
			slog.Info("未检测到可用的 WebView2 运行时，准备降级到 Tier 2 (--app 模式)")
			windows.CloseHandle(windows.Handle(hwnd))
			initCh <- false
			return
		}

		// 修正 2：去除未定义的 cfg.Debug，将上下文菜单选项直接设为 false
		if settings, err := chromium.GetSettings(); err == nil && settings != nil {
			_ = settings.PutAreDefaultContextMenusEnabled(false)
		}
		
		bg := "#F8F9FA"
		fg := "#212529"
		if isDark {
			bg = "#181818"
			fg = "#ECECEC"
		}
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
					document.addEventListener('DOMContentLoaded', inject);
				}
			})();
		`, bg, fg, isDark)
		chromium.Init(bgScript)

		chromium.Navigate(finalURL)
		chromium.Resize()

		var showOnce sync.Once
		presentWindow := func() {
			showOnce.Do(func() {
				sys.FocusWindowSilky(hwnd)[cite: 3]
				emitEvent(eventCh, EventReady)
			})
		}

		chromium.MessageCallback = func(msg string) {
			if msg == `__ready__` {
				presentWindow()
			}
		}
		chromium.Init(`
			window.addEventListener('DOMContentLoaded', () => {
				if (window.chrome && window.chrome.webview) {
					window.chrome.webview.postMessage('__ready__');
				}
			});
		`)

		time.AfterFunc(250*time.Millisecond, func() {
			presentWindow()
		})

		initCh <- true

		defer func() {
			m.hwnd.Store(0)
			globalChromiumMap.Delete(hwnd)
			procPostMessageW.Call(hwnd, wmClose, 0, 0)
		}()

		var mMsg msg
		for {
			ret, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&mMsg)), 0, 0, 0)
			if int32(ret) <= 0 {
				break
			}
			procTranslateMessage.Call(uintptr(unsafe.Pointer(&mMsg)))
			procDispatchMessageW.Call(uintptr(unsafe.Pointer(&mMsg)))
		}
	}()

	return <-initCh
}
