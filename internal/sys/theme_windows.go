package sys

import (
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var (
	modDwmapi                 = windows.NewLazySystemDLL("dwmapi.dll")
	procDwmSetWindowAttribute = modDwmapi.NewProc("DwmSetWindowAttribute")
)

const (
	dwmwaUseImmersiveDarkModeBefore20H1 = 19
	dwmwaUseImmersiveDarkMode           = 20
)

func SetWindowDarkMode(hwnd uintptr, enable bool) {
	val := int32(0)
	if enable {
		val = 1
	}

	ret, _, _ := procDwmSetWindowAttribute.Call(
		hwnd,
		dwmwaUseImmersiveDarkMode,
		uintptr(unsafe.Pointer(&val)),
		unsafe.Sizeof(val),
	)

	if ret != 0 {
		_, _, _ = procDwmSetWindowAttribute.Call(
			hwnd,
			dwmwaUseImmersiveDarkModeBefore20H1,
			uintptr(unsafe.Pointer(&val)),
			unsafe.Sizeof(val),
		)
	}
}

func IsSystemDarkMode() bool {
	k, err := registry.OpenKey(
		registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`,
		registry.QUERY_VALUE,
	)
	if err != nil {
		return false
	}
	defer k.Close()

	val, _, err := k.GetIntegerValue("AppsUseLightTheme")
	if err != nil {
		return false
	}
	return val == 0
}
