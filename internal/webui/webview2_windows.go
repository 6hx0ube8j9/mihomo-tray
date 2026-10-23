package webui

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"syscall"
	"unsafe"

	"github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"

	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/sys"
)

const webview2Tag = "webview2"

var (
	iidICoreWebView2Controller2 = windows.GUID{
		Data1: 0xc979903e,
		Data2: 0xd4ca,
		Data3: 0x4228,
		Data4: [8]byte{0x92, 0xeb, 0x47, 0xee, 0x3f, 0xa9, 0x6e, 0x42},
	}
)

const (
	vtableIdxQueryInterface           = 0
	vtableIdxRelease                  = 2
	vtableIdxPutDefaultBackgroundColor = 27
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

		sys.SetWindowIconClean(hwnd, "APP")
		isDark := sys.IsSystemDarkMode()
		if isDark {
			sys.SetWindowDarkMode(hwnd, true)
		}

		if isDark {
			setWebView2BackgroundColor(w, 0x18, 0x18, 0x18, 0xFF)
		} else {
			setWebView2BackgroundColor(w, 0xF8, 0xF9, 0xFA, 0xFF)
		}

		// 3. 内存级 CSS 注入兜底（嗅探面板 localStorage 自定义的主题设置）
		bgScript := fmt.Sprintf(`
			(function() {
				let isDark = %t;
				try {
					const local = localStorage.getItem('theme') || localStorage.getItem('theme-mode');
					if (local) isDark = local.includes('dark');
				} catch(e) {}
				const bg = isDark ? '#181818' : '#F8F9FA';
				const fg = isDark ? '#ECECEC' : '#212529';
				const style = document.createElement('style');
				style.id = '__preload_bg__';
				style.innerHTML = 'html, body { background-color: ' + bg + ' !important; color: ' + fg + '; }';
				const target = document.head || document.documentElement;
				if (target) {
					target.appendChild(style);
				} else {
					document.addEventListener('DOMContentLoaded', () => {
						if (!document.getElementById('__preload_bg__')) {
							(document.head || document.documentElement).appendChild(style);
						}
					});
				}
			})();
		`, isDark)
		// 修正点：w.Init 无返回值，直接单行调用
		w.Init(bgScript)

		// 4. 底色和标题栏全部就绪后再平滑呼出前台
		sys.FocusWindowSilky(hwnd)

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

// setWebView2BackgroundColor 通过 COM 虚函数表安全调用 put_DefaultBackgroundColor
func setWebView2BackgroundColor(w webview2.WebView, r, g, b, a uint8) bool {
	if w == nil {
		return false
	}

	ctrlPtr := getControllerPtr(reflect.ValueOf(w))
	if ctrlPtr == 0 {
		return false
	}

	vtblPtr := *(*uintptr)(unsafe.Pointer(ctrlPtr))
	vtbl := (*[35]uintptr)(unsafe.Pointer(vtblPtr))

	// 查询 ICoreWebView2Controller2 接口
	var ctrl2 uintptr
	hr, _, _ := syscall.SyscallN(
		vtbl[vtableIdxQueryInterface],
		ctrlPtr,
		uintptr(unsafe.Pointer(&iidICoreWebView2Controller2)),
		uintptr(unsafe.Pointer(&ctrl2)),
	)
	if hr != 0 || ctrl2 == 0 {
		return false
	}

	vtbl2Ptr := *(*uintptr)(unsafe.Pointer(ctrl2))
	vtbl2 := (*[35]uintptr)(unsafe.Pointer(vtbl2Ptr))
	defer func() {
		syscall.SyscallN(vtbl2[vtableIdxRelease], ctrl2)
	}()

	// Windows COREWEBVIEW2_COLOR 格式：小端序 (A, R, G, B)
	packedColor := uint32(a) | (uint32(r) << 8) | (uint32(g) << 16) | (uint32(b) << 24)
	putBgColorFunc := vtbl2[vtableIdxPutDefaultBackgroundColor]
	ret, _, _ := syscall.SyscallN(putBgColorFunc, ctrl2, uintptr(packedColor))
	return ret == 0
}

func getControllerPtr(val reflect.Value) uintptr {
	if val.Kind() == reflect.Ptr {
		val = val.Elem()
	}
	if val.Kind() != reflect.Struct {
		return 0
	}

	if f := val.FieldByName("controller"); f.IsValid() && f.CanAddr() {
		return *(*uintptr)(unsafe.Pointer(f.UnsafeAddr()))
	}

	for i := 0; i < val.NumField(); i++ {
		sub := val.Field(i)
		if sub.Kind() == reflect.Ptr {
			sub = sub.Elem()
		}
		if sub.Kind() == reflect.Struct {
			if f := sub.FieldByName("controller"); f.IsValid() && f.CanAddr() {
				return *(*uintptr)(unsafe.Pointer(f.UnsafeAddr()))
			}
		}
	}
	return 0
}
