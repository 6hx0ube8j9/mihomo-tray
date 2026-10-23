package webui

import (
	"reflect"
	"syscall"
	"unsafe"

	"github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
)

var (
	// ICoreWebView2Controller2 接口的官方 GUID: {c979903e-d4ca-4228-92eb-47ee3fa96e42}
	iidICoreWebView2Controller2 = windows.GUID{
		Data1: 0xc979903e,
		Data2: 0xd4ca,
		Data3: 0x4228,
		Data4: [8]byte{0x92, 0xeb, 0x47, 0xee, 0x3f, 0xa9, 0x6e, 0x42},
	}
)

const (
	// VTable 槽位索引
	vtableIdxQueryInterface           = 0
	vtableIdxRelease                  = 2
	vtableIdxPutDefaultBackgroundColor = 27
)

// SetWebView2BackgroundColor 设置 WebView2 原生合成器底色
// r, g, b: 0~255 颜色分量
// a: 0 为完全透明（展示宿主窗口背景），255 为完全不透明
func SetWebView2BackgroundColor(w webview2.WebView, r, g, b, a uint8) bool {
	if w == nil {
		return false
	}

	// 1. 通过反射从 *webview2.browser 结构体提取私有 controller 指针
	val := reflect.ValueOf(w)
	if val.Kind() == reflect.Ptr {
		val = val.Elem()
	}
	if val.Kind() != reflect.Struct {
		return false
	}

	f := val.FieldByName("controller")
	if !f.IsValid() {
		return false
	}

	// 提取 COM 实例指针 (ICoreWebView2Controller*)
	ctrlPtr := *(*uintptr)(unsafe.Pointer(f.UnsafeAddr()))
	if ctrlPtr == 0 {
		return false
	}

	// 2. 获取 ICoreWebView2Controller 的虚函数表 (VTable)
	vtblPtr := *(*uintptr)(unsafe.Pointer(ctrlPtr))
	vtbl := (*[35]uintptr)(unsafe.Pointer(vtblPtr))

	// 3. 调用 QueryInterface 查询 ICoreWebView2Controller2 接口
	var ctrl2 uintptr
	hr, _, _ := syscall.SyscallN(
		vtbl[vtableIdxQueryInterface],
		ctrlPtr,
		uintptr(unsafe.Pointer(&iidICoreWebView2Controller2)),
		uintptr(unsafe.Pointer(&ctrl2)),
	)
	if hr != 0 || ctrl2 == 0 {
		return false // 运行时版本过低不支持 Controller2
	}

	// 查询成功后增加引用计数，执行完毕必须调用 Release
	vtbl2Ptr := *(*uintptr)(unsafe.Pointer(ctrl2))
	vtbl2 := (*[35]uintptr)(unsafe.Pointer(vtbl2Ptr))
	defer func() {
		syscall.SyscallN(vtbl2[vtableIdxRelease], ctrl2)
	}()

	// 4. 打包 COREWEBVIEW2_COLOR (Little-Endian: A, R, G, B)
	packedColor := uint32(a) | (uint32(r) << 8) | (uint32(g) << 16) | (uint32(b) << 24)

	// 5. 调用 put_DefaultBackgroundColor(color)
	putBgColorFunc := vtbl2[vtableIdxPutDefaultBackgroundColor]
	ret, _, _ := syscall.SyscallN(
		putBgColorFunc,
		ctrl2,
		uintptr(packedColor),
	)

	return ret == 0
}
