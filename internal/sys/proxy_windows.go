//go:build windows

package sys

import (
	"context"
	"errors"
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
	internetOptionRefresh             = 37
	internetOptionSettingsChanged     = 39
	internetOptionPerConnectionOption = 75

	internetPerConnFlags       = 1
	internetPerConnProxyServer = 2
	internetPerConnProxyBypass = 3

	proxyTypeDirect = 0x00000001
	proxyTypeProxy  = 0x00000002

	internetSettingsPath = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`

	rasErrorSuccess        = 0
	rasErrorBufferTooSmall = 603
	rasMaxEntryName        = 256
)

type internetPerConnOption struct {
	dwOption uint32
	dwValue  uint64
}

type internetPerConnOptionList struct {
	dwSize        uint32
	pszConnection *uint16
	dwOptionCount uint32
	dwOptionError uint32
	pOptions      uintptr
}

type rasEntryName struct {
	dwSize      uint32
	szEntryName [rasMaxEntryName + 1]uint16
	dwFlags     uint32
	szPhonebook [windows.MAX_PATH + 1]uint16
}

func RefreshSystemProxy() {
	_, _, _ = procInternetSetOption.Call(0, internetOptionSettingsChanged, 0, 0)
	_, _, _ = procInternetSetOption.Call(0, internetOptionRefresh, 0, 0)
}

func GetSystemProxy() (ProxyStatus, error) {
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

func EnableSystemProxy(serverStr string, bypassStr ...string) error {
	server := NormalizeProxyServer(serverStr)
	if server == "" {
		return errors.New("代理地址为空")
	}

	bypass := DefaultProxyOverride
	if len(bypassStr) > 0 && strings.TrimSpace(bypassStr[0]) != "" {
		bypass = strings.TrimSpace(bypassStr[0])
	}

	if curr, err := GetSystemProxy(); err == nil && curr.Enabled && curr.Matches(server) {
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
		{dwOption: internetPerConnFlags, dwValue: uint64(proxyTypeDirect | proxyTypeProxy)},
		{dwOption: internetPerConnProxyServer, dwValue: uint64(uintptr(unsafe.Pointer(serverPtr)))},
		{dwOption: internetPerConnProxyBypass, dwValue: uint64(uintptr(unsafe.Pointer(bypassPtr)))},
	}

	return applyProxyOptions(options)
}

func DisableSystemProxy(expectedServer string) error {
	expected := NormalizeProxyServer(expectedServer)

	if curr, err := GetSystemProxy(); err == nil {
		if !curr.Enabled {
			return nil
		}
		if expected != "" && !curr.Matches(expected) {
			slog.Warn("代理地址不匹配，跳过关闭", "current", curr.Server, "expected", expected)
			return nil
		}
	}

	options := []internetPerConnOption{
		{dwOption: internetPerConnFlags, dwValue: uint64(proxyTypeDirect)},
	}

	return applyProxyOptions(options)
}

func WatchSystemProxy(ctx context.Context, statusCh chan<- ProxyStatus) {
	lastStatus, _ := GetSystemProxy()

	err := watchRegistryKey(ctx, internetSettingsPath, func() {
		status, err := GetSystemProxy()
		if err != nil || status == lastStatus {
			return
		}
		lastStatus = status
		select {
		case statusCh <- status:
		case <-ctx.Done():
		}
	})
	if err != nil && ctx.Err() == nil {
		slog.Warn("代理注册表监听失败", "err", err)
	}
}

func setPerConnectionOption(list *internetPerConnOptionList, connName *uint16) error {
	list.pszConnection = connName
	r, _, err := procInternetSetOption.Call(
		0,
		internetOptionPerConnectionOption,
		uintptr(unsafe.Pointer(list)),
		uintptr(list.dwSize),
	)
	if r == 0 {
		return fmt.Errorf("设置系统代理选项失败: %w", err)
	}
	return nil
}

func applyProxyToList(list *internetPerConnOptionList) error {
	if err := setPerConnectionOption(list, nil); err != nil {
		return err
	}

	entrySize := uint32(unsafe.Sizeof(rasEntryName{}))
	cb := entrySize
	var cEntries uint32
	var entry rasEntryName
	entry.dwSize = entrySize

	ret, _, _ := procRasEnumEntries.Call(
		0, 0,
		uintptr(unsafe.Pointer(&entry)),
		uintptr(unsafe.Pointer(&cb)),
		uintptr(unsafe.Pointer(&cEntries)),
	)

	if ret == rasErrorSuccess && cEntries == 0 {
		return nil
	}

	if ret == rasErrorSuccess && cEntries == 1 {
		_ = setPerConnectionOption(list, &entry.szEntryName[0])
		runtime.KeepAlive(&entry)
		return nil
	}

	if ret == rasErrorBufferTooSmall {
		reqCount := (cb + entrySize - 1) / entrySize
		if reqCount > 0 {
			entries := make([]rasEntryName, reqCount)
			for i := range entries {
				entries[i].dwSize = entrySize
			}

			ret, _, _ = procRasEnumEntries.Call(
				0, 0,
				uintptr(unsafe.Pointer(&entries[0])),
				uintptr(unsafe.Pointer(&cb)),
				uintptr(unsafe.Pointer(&cEntries)),
			)

			if ret == rasErrorSuccess {
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

	RefreshSystemProxy()
	return nil
}