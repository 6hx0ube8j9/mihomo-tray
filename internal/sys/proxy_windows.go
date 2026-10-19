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
	procInternetSetOption = modWininet.NewProc("InternetSetOptionW")
	procRasEnumEntries    = modRasapi32.NewProc("RasEnumEntriesW")
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

	internetSettingsPath = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`
	connectionsPath      = internetSettingsPath + `\Connections`

	ERROR_SUCCESS          = 0
	ERROR_BUFFER_TOO_SMALL = 603
	RAS_MaxEntryName       = 256
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
	szPhonebook [windows.MAX_PATH + 1]uint16
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

func EnableProxy(serverStr string, bypassStr ...string) error {
	server := NormalizeProxyServer(serverStr)
	if server == "" {
		return fmt.Errorf("proxy server empty")
	}

	bypass := DefaultProxyOverride
	if len(bypassStr) > 0 && strings.TrimSpace(bypassStr[0]) != "" {
		bypass = strings.TrimSpace(bypassStr[0])
	}

	if curr, err := GetProxyStatus(); err == nil && curr.Enabled && curr.Matches(server) {
		return nil
	}

	serverPtr, err := windows.UTF16PtrFromString(server)
	if err != nil {
		return err
	}
	bypassPtr, err := windows.UTF16PtrFromString(bypass)
	if err != nil {
		return err
	}

	options := []internetPerConnOption{
		{dwOption: INTERNET_PER_CONN_FLAGS, dwValue: uintptr(PROXY_TYPE_DIRECT | PROXY_TYPE_PROXY)},
		{dwOption: INTERNET_PER_CONN_PROXY_SERVER, dwValue: uintptr(unsafe.Pointer(serverPtr))},
		{dwOption: INTERNET_PER_CONN_PROXY_BYPASS, dwValue: uintptr(unsafe.Pointer(bypassPtr))},
	}

	defer func() {
		runtime.KeepAlive(serverPtr)
		runtime.KeepAlive(bypassPtr)
	}()

	if err := applyProxyOptions(options); err != nil {
		slog.Error("启用系统代理失败", "err", err)
		return err
	}

	slog.Info("系统代理已开启", "server", server)
	return nil
}

func DisableProxy(expectedServer string) error {
	expected := NormalizeProxyServer(expectedServer)

	curr, err := GetProxyStatus()
	if err == nil {
		if !curr.Enabled {
			return nil
		}
		if expected != "" && !curr.Matches(expected) {
			slog.Warn("跳过注销代理", "reason", "代理地址被外部修改", "current", curr.Server, "expected", expected)
			return nil
		}
	}

	options := []internetPerConnOption{
		{dwOption: INTERNET_PER_CONN_FLAGS, dwValue: uintptr(PROXY_TYPE_DIRECT)},
	}

	if err := applyProxyOptions(options); err != nil {
		slog.Error("停用系统代理失败", "err", err)
		return err
	}

	slog.Info("系统代理已注销")
	return nil
}

func setPerConnectionOption(list *internetPerConnOptionList, connName *uint16) error {
	list.pszConnection = connName
	r, _, err := procInternetSetOption.Call(
		0,
		INTERNET_OPTION_PER_CONNECTION_OPTION,
		uintptr(unsafe.Pointer(list)),
		uintptr(list.dwSize),
	)
	if r == 0 {
		return fmt.Errorf("InternetSetOptionW 失败: %w", err)
	}
	return nil
}

func applyProxyToList(list *internetPerConnOptionList) error {
	if err := setPerConnectionOption(list, nil); err != nil {
		return err
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

	if ret == uintptr(ERROR_BUFFER_TOO_SMALL) && cEntries > 0 {
		entries := make([]RasEntryName, cEntries)
		for i := range entries {
			entries[i].dwSize = cb
		}
		ret, _, _ = procRasEnumEntries.Call(
			0, 0,
			uintptr(unsafe.Pointer(&entries[0])),
			uintptr(unsafe.Pointer(&cb)),
			uintptr(unsafe.Pointer(&cEntries)),
		)
		if ret == uintptr(ERROR_SUCCESS) {
			for i := range entries {
				_ = setPerConnectionOption(list, &entries[i].szEntryName[0])
			}
			runtime.KeepAlive(entries)
		}
	} else if ret == uintptr(ERROR_SUCCESS) && cEntries == 1 {
		_ = setPerConnectionOption(list, &entry.szEntryName[0])
		runtime.KeepAlive(&entry)
	}

	return nil
}

func applyProxyOptions(options []internetPerConnOption) error {
	if len(options) == 0 {
		return nil
	}

	var list internetPerConnOptionList
	list.dwSize = uint32(unsafe.Sizeof(list))
	list.dwOptionCount = uint32(len(options))
	list.pOptions = uintptr(unsafe.Pointer(&options[0]))

	if err := applyProxyToList(&list); err != nil {
		return err
	}

	RefreshWininet()
	return nil
}

func WatchProxyRegistry(ctx context.Context, statusCh chan<- ProxyStatus) {
	paths := []string{internetSettingsPath, connectionsPath}
	var keys []registry.Key
	var handles []windows.Handle

	defer func() {
		for _, h := range handles {
			if h != 0 {
				_ = windows.CloseHandle(h)
			}
		}
		for _, k := range keys {
			_ = k.Close()
		}
	}()

	for _, path := range paths {
		k, err := registry.OpenKey(registry.CURRENT_USER, path, registry.NOTIFY|registry.QUERY_VALUE)
		if err != nil {
			slog.Warn("打开代理注册表监听失败", "path", path, "err", err)
			continue
		}
		keys = append(keys, k)

		event, err := windows.CreateEvent(nil, 0, 0, nil)
		if err != nil {
			return
		}
		handles = append(handles, event)
	}

	if len(handles) == 0 {
		return
	}

	cancelEvent, err := windows.CreateEvent(nil, 0, 0, nil)
	if err != nil {
		return
	}
	handles = append(handles, cancelEvent)
	cancelIdx := uint32(len(handles) - 1)

	stopCtxNotify := make(chan struct{})
	defer close(stopCtxNotify)

	go func() {
		select {
		case <-ctx.Done():
			_ = windows.SetEvent(cancelEvent)
		case <-stopCtxNotify:
		}
	}()

	armKey := func(idx int) {
		filter := uint32(windows.REG_NOTIFY_CHANGE_LAST_SET | windows.REG_NOTIFY_CHANGE_NAME | windows.REG_NOTIFY_THREAD_AGNOSTIC)
		if err := windows.RegNotifyChangeKeyValue(windows.Handle(keys[idx]), false, filter, handles[idx], true); err != nil {
			filter &^= windows.REG_NOTIFY_THREAD_AGNOSTIC
			_ = windows.RegNotifyChangeKeyValue(windows.Handle(keys[idx]), false, filter, handles[idx], true)
		}
	}

	for i := range keys {
		armKey(i)
	}

	for {
		index, err := windows.WaitForMultipleObjects(handles, false, windows.INFINITE)
		if err != nil || index == windows.WAIT_OBJECT_0+cancelIdx {
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
