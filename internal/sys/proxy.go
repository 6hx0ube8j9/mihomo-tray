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

	modRasapi32        = windows.NewLazySystemDLL("rasapi32.dll")
	procRasEnumEntries = modRasapi32.NewProc("RasEnumEntriesW")
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

	ERROR_SUCCESS          = 0
	ERROR_BUFFER_TOO_SMALL = 603
	RAS_MaxEntryName       = 256
	MAX_PATH               = 260
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

type RasEntryName struct {
	dwSize      uint32
	szEntryName [RAS_MaxEntryName + 1]uint16
	dwFlags     uint32
	szPhonebook [MAX_PATH + 1]uint16
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

func applyProxyToList(list *internetPerConnOptionList) error {
	list.pszConnection = nil
	r, _, err := procInternetSetOption.Call(
		0,
		INTERNET_OPTION_PER_CONNECTION_OPTION,
		uintptr(unsafe.Pointer(list)),
		uintptr(list.dwSize),
	)
	if r == 0 {
		return fmt.Errorf("InternetSetOptionW 设置 LAN 代理失败: %w", err)
	}

	var cb uint32 = uint32(unsafe.Sizeof(RasEntryName{}))
	var cEntries uint32 = 0
	entry := RasEntryName{dwSize: cb}

	ret, _, _ := procRasEnumEntries.Call(
		0, 0,
		uintptr(unsafe.Pointer(&entry)),
		uintptr(unsafe.Pointer(&cb)),
		uintptr(unsafe.Pointer(&cEntries)),
	)

	if ret == ERROR_SUCCESS && cEntries == 1 {
		list.pszConnection = &entry.szEntryName[0]
		procInternetSetOption.Call(
			0,
			INTERNET_OPTION_PER_CONNECTION_OPTION,
			uintptr(unsafe.Pointer(list)),
			uintptr(list.dwSize),
		)
		runtime.KeepAlive(&entry)
	} else if ret == ERROR_BUFFER_TOO_SMALL && cEntries > 0 {
		entries := make([]RasEntryName, cEntries)
		entries[0].dwSize = uint32(unsafe.Sizeof(RasEntryName{}))

		ret, _, _ = procRasEnumEntries.Call(
			0, 0,
			uintptr(unsafe.Pointer(&entries[0])),
			uintptr(unsafe.Pointer(&cb)),
			uintptr(unsafe.Pointer(&cEntries)),
		)

		if ret == ERROR_SUCCESS {
			for i := uint32(0); i < cEntries; i++ {
				list.pszConnection = &entries[i].szEntryName[0]
				procInternetSetOption.Call(
					0,
					INTERNET_OPTION_PER_CONNECTION_OPTION,
					uintptr(unsafe.Pointer(list)),
					uintptr(list.dwSize),
				)
			}
		}
		runtime.KeepAlive(entries)
	}

	return nil
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

	if !enable {
		options[0].dwOption = INTERNET_PER_CONN_FLAGS
		options[0].dwValue = uintptr(PROXY_TYPE_DIRECT)

		list.dwOptionCount = 1
		list.pOptions = uintptr(unsafe.Pointer(&options[0]))

		if err := applyProxyToList(&list); err != nil {
			slog.Error("系统代理关闭失败", "err", err)
			return err
		}
		slog.Debug("系统代理已关闭")
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

		if err := applyProxyToList(&list); err != nil {
			slog.Error("系统代理开启失败", "err", err)
			return err
		}

		runtime.KeepAlive(serverPtr)
		runtime.KeepAlive(bypassPtr)

		slog.Debug("系统代理已开启", "Server", expectedServer)
	}

	runtime.KeepAlive(&options)
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
	}
	defer cleanup()

	for _, path := range paths {
		k, err := registry.OpenKey(registry.CURRENT_USER, path, registry.NOTIFY|registry.QUERY_VALUE)
		if err != nil {
			slog.Error("启动注册表监听失败", "path", path, "err", err)
			continue
		}
		keys = append(keys, k)

		event, err := windows.CreateEvent(nil, 0, 0, nil)
		if err != nil {
			slog.Error("创建系统代理监听事件失败", "err", err)
			return
		}
		handles = append(handles, event)
	}

	if len(handles) == 0 {
		return
	}

	cancelEvent, err := windows.CreateEvent(nil, 0, 0, nil)
	if err != nil {
		slog.Error("创建监听取消事件失败", "err", err)
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
			return
		}

		if index == windows.WAIT_OBJECT_0+cancelIdx {
			return
		}

		triggeredIdx := int(index - windows.WAIT_OBJECT_0)
		if triggeredIdx >= 0 && triggeredIdx < len(keys) {
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
