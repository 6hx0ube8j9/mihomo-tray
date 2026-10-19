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

	defer func() {
		runtime.KeepAlive(serverPtr)
		runtime.KeepAlive(bypassPtr)
	}()

	options := []internetPerConnOption{
		{dwOption: INTERNET_PER_CONN_FLAGS, dwValue: uintptr(PROXY_TYPE_DIRECT | PROXY_TYPE_PROXY)},
		{dwOption: INTERNET_PER_CONN_PROXY_SERVER, dwValue: uintptr(unsafe.Pointer(serverPtr))},
		{dwOption: INTERNET_PER_CONN_PROXY_BYPASS, dwValue: uintptr(unsafe.Pointer(bypassPtr))},
	}

	if err := applyProxyOptions(options); err != nil {
		slog.Error("failed to enable proxy", "err", err)
		return err
	}

	slog.Info("proxy enabled", "server", server)
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
			slog.Warn("skip proxy disable", "reason", "address mismatch", "current", curr.Server, "expected", expected)
			return nil
		}
	}

	options := []internetPerConnOption{
		{dwOption: INTERNET_PER_CONN_FLAGS, dwValue: uintptr(PROXY_TYPE_DIRECT)},
	}

	if err := applyProxyOptions(options); err != nil {
		slog.Error("failed to disable proxy", "err", err)
		return err
	}

	slog.Info("proxy disabled")
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
		return fmt.Errorf("InternetSetOptionW failed: %w", err)
	}
	return nil
}

func applyProxyToList(list *internetPerConnOptionList) error {
	if err := setPerConnectionOption(list, nil); err != nil {
		return err
	}

	var entrySize uint32 = uint32(unsafe.Sizeof(RasEntryName{}))
	var cb uint32 = entrySize
	var cEntries uint32
	var entry RasEntryName
	entry.dwSize = entrySize

	ret, _, _ := procRasEnumEntries.Call(
		0, 0,
		uintptr(unsafe.Pointer(&entry)),
		uintptr(unsafe.Pointer(&cb)),
		uintptr(unsafe.Pointer(&cEntries)),
	)

	if ret == uintptr(ERROR_SUCCESS) && cEntries == 0 {
		return nil
	}

	if ret == uintptr(ERROR_SUCCESS) && cEntries == 1 {
		_ = setPerConnectionOption(list, &entry.szEntryName[0])
		runtime.KeepAlive(&entry)
		return nil
	}

	if ret == uintptr(ERROR_BUFFER_TOO_SMALL) {
		reqCount := cb / entrySize
		if reqCount > 0 {
			entries := make([]RasEntryName, reqCount)
			for i := range entries {
				entries[i].dwSize = entrySize
			}

			ret, _, _ = procRasEnumEntries.Call(
				0, 0,
				uintptr(unsafe.Pointer(&entries[0])),
				uintptr(unsafe.Pointer(&cb)),
				uintptr(unsafe.Pointer(&cEntries)),
			)

			if ret == uintptr(ERROR_SUCCESS) {
				limit := int(cEntries)
				if limit > len(entries) {
					limit = len(entries)
				}
				for i := 0; i < limit; i++ {
					_ = setPerConnectionOption(list, &entries[i].szEntryName[0])
				}
			}
			runtime.KeepAlive(entries)
		}
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

	err := applyProxyToList(&list)

	runtime.KeepAlive(options)

	if err != nil {
		return err
	}

	RefreshWininet()
	return nil
}

func WatchProxyRegistry(ctx context.Context, statusCh chan<- ProxyStatus) {
	k, err := registry.OpenKey(registry.CURRENT_USER, internetSettingsPath, registry.NOTIFY|registry.QUERY_VALUE)
	if err != nil {
		slog.Warn("打开代理注册表监听失败", "err", err)
		return
	}
	defer k.Close()

	notifyEvent, err := windows.CreateEvent(nil, 0, 0, nil)
	if err != nil {
		return
	}
	defer windows.CloseHandle(notifyEvent)

	cancelEvent, err := windows.CreateEvent(nil, 0, 0, nil)
	if err != nil {
		return
	}
	defer windows.CloseHandle(cancelEvent)

	stopCancel := context.AfterFunc(ctx, func() {
		_ = windows.SetEvent(cancelEvent)
	})
	defer stopCancel()

	arm := func() {
		filter := uint32(windows.REG_NOTIFY_CHANGE_LAST_SET | windows.REG_NOTIFY_CHANGE_NAME | windows.REG_NOTIFY_THREAD_AGNOSTIC)
		if err := windows.RegNotifyChangeKeyValue(windows.Handle(k), true, filter, notifyEvent, true); err != nil {
			filter &^= windows.REG_NOTIFY_THREAD_AGNOSTIC
			_ = windows.RegNotifyChangeKeyValue(windows.Handle(k), true, filter, notifyEvent, true)
		}
	}

	arm()

	handles := [2]windows.Handle{notifyEvent, cancelEvent}

	const debounceMs = 150
	waitTimeout := uint32(windows.INFINITE)
	hasPendingUpdate := false

	for {
		event, err := windows.WaitForMultipleObjects(handles[:], false, waitTimeout)
		if err != nil || event == windows.WAIT_OBJECT_0+1 {
			return
		}

		switch event {
		case windows.WAIT_OBJECT_0:
			arm()
			waitTimeout = debounceMs
			hasPendingUpdate = true

		case uint32(windows.WAIT_TIMEOUT):
			waitTimeout = windows.INFINITE
			if hasPendingUpdate {
				hasPendingUpdate = false
				if status, err := GetProxyStatus(); err == nil {
					select {
					case statusCh <- status:
					default:
					}
				}
			}
		}
	}
}
