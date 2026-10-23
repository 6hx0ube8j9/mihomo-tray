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

	// Win32 常量
	wsOverlappedWindow = 0x00CF0000
	wsClipChildren     = 0x02000000
	swShow             = 5
	wmSize             = 0x0005
	wmDestroy          = 0x0002
	wmClose            = 0x0010
	idcArrow           = 32512
)

var (
	modUser32            = windows.NewLazySystemDLL("user32.dll")
	modGdi32             = windows.NewLazySystemDLL("gdi32.dll")
	modOle32             = windows.NewLazySystemDLL("ole32.dll")

	procRegisterClassExW = modUser32.NewProc("RegisterClassExW")
	procCreateWindowExW  = modUser32.NewProc("CreateWindowExW")
	procDefWindowProcW   = modUser32.NewProc("DefWindowProcW")
	procDestroyWindow    = modUser32.NewProc("DestroyWindow")
	procShowWindow       = modUser32.NewProc("ShowWindow")
	procPostQuitMessage  = modUser32.NewProc("PostQuitMessage")
	procGetMessageW      = modUser32.NewProc("GetMessageW")
	procTranslateMessage = modUser32.NewProc("TranslateMessage")
	procDispatchMessageW = modUser32.NewProc("DispatchMessageW")
	procPostMessageW     = modUser32.NewProc("PostMessageW")
	procLoadCursorW      = modUser32.NewProc("LoadCursorW")

	procCreateSolidBrush = modGdi32.NewProc("CreateSolidBrush")
	procOleInitialize    = modOle32.NewProc("OleInitialize")
	procOleUninitialize  = modOle32.NewProc("OleUninitialize")

	wndClassOnce sync.Once
	wndClassName = "MihomoWebUIPanel"

	globalChromiumMap sync.Map
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

// wndProc 的 4 个参数必须全部严格为 uintptr，否则 NewCallback 会直接 panic
func wndProc(hwnd, message, wParam, lParam uintptr) uintptr {
	defer func() {
		_ = recover() // 保护 Win32 消息泵，防止任何异常刺穿进程
	}()

	switch message {
	case wmSize:
		if val, ok := globalChromiumMap.Load(hwnd); ok {
			if c, ok := val.(*edge.Chromium); ok && c != nil {
				c.Resize()
			}
		}
		return 0
	case wmDestroy:
		globalChromiumMap.Delete(hwnd)
		procPostQuitMessage.Call(0)
		return 0
	}
	ret, _, _ := procDefWindowProcW.Call(hwnd, message, wParam, lParam)
	return ret
}

func registerPanelClass(hInst windows.Handle, isDark bool) {
	wndClassOnce.Do(func() {
		// 1. GDI 层面使用深色画刷填充原生背景（杜绝系统默认的白色画刷）
		var bgBrush uintptr
		if isDark {
			bgBrush, _, _ = procCreateSolidBrush.Call(0x00181818) // RGB(24, 24, 24)
		} else {
			bgBrush, _, _ = procCreateSolidBrush.Call(0x00FAF9F8)
		}

		cursor, _, _ := procLoadCursorW.Call(0, idcArrow)
		clsNamePtr, _ := windows.UTF16PtrFromString(wndClassName)

		wc := wndClassExW{
			cbSize:        uint32(unsafe.Sizeof(wndClassExW{})),
			lpfnWndProc:   windows.NewCallback(wndProc), // 全 uintptr 签名，安全无 Panic
			hInstance:     hInst,
			hCursor:       windows.Handle(cursor),
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

		// 初始化 COM STA 套间，满足 WebView2 线程模型要求
		procOleInitialize.Call(0)
		defer procOleUninitialize.Call()

		winW, winH, startX, startY := calculateWindowBounds()
		userDataDir := filepath.Join(cfg.BaseDir, domain.WebCacheDir, webview2Tag)
		_ = os.MkdirAll(userDataDir, 0755)

		sys.GrantForegroundPrivilege()

		var hInst windows.Handle
		_ = windows.GetModuleHandleEx(0, nil, &hInst)

		isDark := sys.IsSystemDarkMode()
		registerPanelClass(hInst, isDark)

		clsNamePtr, _ := windows.UTF16PtrFromString(wndClassName)
		titlePtr, _ := windows.UTF16PtrFromString(domain.AppName)

		// 核心机制：创建窗口时坚决不传 WS_VISIBLE，窗口在内存中完全静默
		hwndRet, _, _ := procCreateWindowExW.Call(
			0,
			uintptr(unsafe.Pointer(clsNamePtr)),
			uintptr(unsafe.Pointer(titlePtr)),
			wsOverlappedWindow|wsClipChildren,
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

		// 初始化底层 Chromium
		chromium := edge.NewChromium()
		chromium.DataPath = userDataDir
		chromium.SetPermission(edge.CoreWebView2PermissionKindClipboardRead, edge.CoreWebView2PermissionStateAllow)

		// 嵌入窗口
		if !chromium.Embed(hwnd) {
			slog.Info("未检测到可用的 WebView2 运行时，准备降级到 Tier 2 (--app 模式)")
			procDestroyWindow.Call(hwnd)
			initCh <- false
			return
		}

		globalChromiumMap.Store(hwnd, chromium)
		chromium.Resize()

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

		var showOnce sync.Once
		presentWindow := func() {
			showOnce.Do(func() {
				procShowWindow.Call(hwnd, swShow)
				sys.FocusWindowSilky(hwnd)
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

		// 250ms 超时强制唤醒兜底
		time.AfterFunc(250*time.Millisecond, func() {
			presentWindow()
		})

		initCh <- true

		defer func() {
			m.hwnd.Store(0)
			globalChromiumMap.Delete(hwnd)
			procPostMessageW.Call(hwnd, wmClose, 0, 0)
		}()

		// 标准 Win32 消息循环
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
