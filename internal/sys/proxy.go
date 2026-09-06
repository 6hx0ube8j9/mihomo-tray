package sys

import (
	"context"
	"fmt"
	"log/slog"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var (
	modWininet            = windows.NewLazySystemDLL("wininet.dll")
	procInternetSetOption = modWininet.NewProc("InternetSetOptionW")
)

const (
	INTERNET_OPTION_REFRESH               = 37
	INTERNET_OPTION_SETTINGS_CHANGED      = 39
	INTERNET_OPTION_PER_CONNECTION_OPTION = 75

	INTERNET_PER_CONN_FLAGS        = 1
	INTERNET_PER_CONN_PROXY_SERVER = 2
	INTERNET_PER_CONN_PROXY_BYPASS = 3

	PROXY_TYPE_DIRECT = 0x00000001
	PROXY_TYPE_PROXY  = 0x00000002

	defaultProxyOverride = "<local>;localhost;127.*;10.*;172.16.*;172.17.*;172.18.*;172.19.*;172.20.*;172.21.*;172.22.*;172.23.*;172.24.*;172.25.*;172.26.*;172.27.*;172.28.*;172.29.*;172.30.*;172.31.*;192.168.*"

	internetSettingsPath = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`
	connectionsPath      = internetSettingsPath + `\Connections`
)

type internetPerConnOption struct {
	dwOption uint32
	dwValue  uintptr
}

type internetPerConnOptionList struct {
	dwSize        uint32
	pszConnection *uint16
	dwOptionCount uint32
	dwOptionError uint32
	pOptions      uintptr
}

type ProxyStatus struct {
	Enabled bool
	Server  string
}

func RefreshWininet() {
	_, _, _ = procInternetSetOption.Call(0, INTERNET_OPTION_SETTINGS_CHANGED, 0, 0)
	_, _, _ = procInternetSetOption.Call(0, INTERNET_OPTION_REFRESH, 0, 0)
}

func GetProxyStatus() (ProxyStatus, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, internetSettingsPath, registry.QUERY_VALUE)
	if err != nil {
		slog.Error("读取系统代理注册表失败", "err", err)
		return ProxyStatus{}, err
	}
	defer k.Close()

	val, _, err := k.GetIntegerValue("ProxyEnable")
	server, _, _ := k.GetStringValue("ProxyServer")

	return ProxyStatus{
		Enabled: err == nil && val == 1,
		Server:  server,
	}, nil
}

func SetSystemProxy(enable bool, portStr string) error {
	curr, err := GetProxyStatus()
	if err == nil {
		if !enable && !curr.Enabled {
			return nil
		}
		if enable {
			port := strings.TrimSpace(portStr)
			expectedServer := "127.0.0.1:" + port
			if curr.Enabled && strings.EqualFold(curr.Server, expectedServer) {
				return nil
			}
		}
	}

	var options [3]internetPerConnOption
	var list internetPerConnOptionList

	list.dwSize = uint32(unsafe.Sizeof(list))
	list.pszConnection = nil

	if !enable {
		options[0].dwOption = INTERNET_PER_CONN_FLAGS
		options[0].dwValue = uintptr(PROXY_TYPE_DIRECT)

		list.dwOptionCount = 1
		list.pOptions = uintptr(unsafe.Pointer(&options[0]))

		r, _, err := procInternetSetOption.Call(
			0,
			INTERNET_OPTION_PER_CONNECTION_OPTION,
			uintptr(unsafe.Pointer(&list)),
			uintptr(list.dwSize),
		)
		
		runtime.KeepAlive(&options)
		
		if r == 0 {
			slog.Error("WinINet 关闭代理失败", "err", err)
			return fmt.Errorf("InternetSetOptionW 关闭代理失败: %w", err)
		}
		slog.Debug("底层动作: 系统代理已关闭")
	} else {
		port := strings.TrimSpace(portStr)
		if port == "" {
			return fmt.Errorf("proxy port cannot be empty")
		}
		expectedServer := "127.0.0.1:" + port

		serverPtr, err := windows.UTF16PtrFromString(expectedServer)
		if err != nil {
			return fmt.Errorf("构建代理地址字符串失败: %w", err)
		}
		bypassPtr, err := windows.UTF16PtrFromString(defaultProxyOverride)
		if err != nil {
			return fmt.Errorf("构建绕过白名单字符串失败: %w", err)
		}

		options[0].dwOption = INTERNET_PER_CONN_FLAGS
		options[0].dwValue = uintptr(PROXY_TYPE_DIRECT | PROXY_TYPE_PROXY)

		options[1].dwOption = INTERNET_PER_CONN_PROXY_SERVER
		options[1].dwValue = uintptr(unsafe.Pointer(serverPtr))
		options[2].dwOption = INTERNET_PER_CONN_PROXY_BYPASS
		options[2].dwValue = uintptr(unsafe.Pointer(bypassPtr))

		list.dwOptionCount = 3
		list.pOptions = uintptr(unsafe.Pointer(&options[0]))

		r, _, err := procInternetSetOption.Call(
			0,
			INTERNET_OPTION_PER_CONNECTION_OPTION,
			uintptr(unsafe.Pointer(&list)),
			uintptr(list.dwSize),
		)

		runtime.KeepAlive(serverPtr)
		runtime.KeepAlive(bypassPtr)
		runtime.KeepAlive(&options)

		if r == 0 {
			slog.Error("WinINet 设置代理失败", "err", err)
			return fmt.Errorf("InternetSetOptionW 设置代理失败: %w", err)
		}
		slog.Debug("底层动作: 系统代理已开启", "目标Server", expectedServer)
	}

	RefreshWininet()
	return nil
}

func WatchProxyRegistry(ctx context.Context, statusCh chan<- ProxyStatus) {
	paths := []string{internetSettingsPath, connectionsPath}
	var keys []registry.Key
	var handles []windows.Handle

	cleanup := func() {
		for _, h := range handles {
			if h != 0 {
				_ = windows.CloseHandle(h)
			}
		}
		for _, k := range keys {
			_ = k.Close()
		}
		slog.Debug("系统代理注册表监听已退出清理")
	}
	defer cleanup()

	for _, path := range paths {
		k, err := registry.OpenKey(registry.CURRENT_USER, path, registry.NOTIFY|registry.QUERY_VALUE)
		if err != nil {
			slog.Error("启动注册表监听失败 (无法打开键)", "path", path, "err", err)
			continue
		}
		keys = append(keys, k)

		event, err := windows.CreateEvent(nil, 0, 0, nil)
		if err != nil {
			slog.Error("创建 Windows Event 句柄失败", "err", err)
			return
		}
		handles = append(handles, event)
	}

	if len(handles) == 0 {
		return
	}

	slog.Debug("系统代理注册表监听已启动", "监听路径数量", len(keys))

	cancelEvent, err := windows.CreateEvent(nil, 0, 0, nil)
	if err != nil {
		slog.Error("创建 Cancel Event 失败", "err", err)
		return
	}
	handles = append(handles, cancelEvent)
	cancelIdx := uint32(len(handles) - 1)

	go func() {
		<-ctx.Done()
		_ = windows.SetEvent(cancelEvent)
	}()

	armKey := func(idx int) {
		filter := uint32(windows.REG_NOTIFY_CHANGE_LAST_SET | windows.REG_NOTIFY_CHANGE_NAME | windows.REG_NOTIFY_THREAD_AGNOSTIC)
		err := windows.RegNotifyChangeKeyValue(windows.Handle(keys[idx]), false, filter, handles[idx], true)
		if err != nil {
			filter &^= windows.REG_NOTIFY_THREAD_AGNOSTIC
			_ = windows.RegNotifyChangeKeyValue(windows.Handle(keys[idx]), false, filter, handles[idx], true)
		}
	}

	for i := range keys {
		armKey(i)
	}

	for {
		index, err := windows.WaitForMultipleObjects(handles, false, windows.INFINITE)
		if err != nil {
			slog.Error("WaitForMultipleObjects 等待注册表事件出错", "err", err)
			return
		}

		if index == windows.WAIT_OBJECT_0+cancelIdx {
			return
		}

		triggeredIdx := int(index - windows.WAIT_OBJECT_0)
		if triggeredIdx >= 0 && triggeredIdx < len(keys) {
			slog.Debug("检测到系统代理注册表发生变更")
			armKey(triggeredIdx)
		} else {
			for i := range keys {
				armKey(i)
			}
		}

		if status, err := GetProxyStatus(); err == nil {
			select {
			case statusCh <- status:
			default:
			}
		}
	}
}
