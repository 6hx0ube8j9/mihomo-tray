package sys

import (
	"context"
	"fmt"
	"strings"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	INTERNET_OPTION_REFRESH          = 37
	INTERNET_OPTION_SETTINGS_CHANGED = 39

	defaultProxyOverride = "<local>;localhost;127.*;10.*;172.16.*;172.17.*;172.18.*;172.19.*;172.20.*;172.21.*;172.22.*;172.23.*;172.24.*;172.25.*;172.26.*;172.27.*;172.28.*;172.29.*;172.30.*;172.31.*;192.168.*"

	internetSettingsPath = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`
	connectionsPath      = internetSettingsPath + `\Connections`
)

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

func SetSystemProxy(enable bool, portStr string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, internetSettingsPath, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()

	currEnable, _, errEnable := k.GetIntegerValue("ProxyEnable")
	currServer, _, errServer := k.GetStringValue("ProxyServer")

	if !enable {
		if errEnable == nil && currEnable == 0 {
			return nil
		}
		_ = k.SetDWordValue("ProxyEnable", 0)
		RefreshWininet()
		return nil
	}

	port := strings.TrimSpace(portStr)
	if port == "" {
		return fmt.Errorf("proxy port cannot be empty")
	}
	expectedServer := "127.0.0.1:" + port

	if errEnable == nil && currEnable == 1 && errServer == nil && strings.EqualFold(currServer, expectedServer) {
		return nil
	}

	_ = k.SetDWordValue("ProxyEnable", 1)
	_ = k.SetStringValue("ProxyServer", expectedServer)
	_ = k.SetStringValue("ProxyOverride", defaultProxyOverride)
	_ = k.SetDWordValue("AutoDetect", 0)
	_ = k.DeleteValue("AutoConfigURL")

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
			continue
		}
		keys = append(keys, k)

		event, err := windows.CreateEvent(nil, 0, 0, nil)
		if err != nil {
			return
		}
		handles = append(handles, event)

		filter := uint32(windows.REG_NOTIFY_CHANGE_LAST_SET | windows.REG_NOTIFY_CHANGE_NAME | windows.REG_NOTIFY_THREAD_AGNOSTIC)
		err = windows.RegNotifyChangeKeyValue(windows.Handle(k), false, filter, event, true)
		if err != nil {
			filter &^= windows.REG_NOTIFY_THREAD_AGNOSTIC
			_ = windows.RegNotifyChangeKeyValue(windows.Handle(k), false, filter, event, true)
		}
	}

	if len(handles) == 0 {
		return
	}

	cancelEvent, _ := windows.CreateEvent(nil, 0, 0, nil)
	handles = append(handles, cancelEvent)

	go func() {
		<-ctx.Done()
		_ = windows.SetEvent(cancelEvent)
	}()

	for {
		index, err := windows.WaitForMultipleObjects(handles, false, windows.INFINITE)
		if err != nil {
			return
		}

		if index == windows.WAIT_OBJECT_0+uint32(len(handles)-1) {
			return
		}

		if status, err := GetProxyStatus(); err == nil {
			select {
			case statusCh <- status:
			case <-ctx.Done():
				return
			}
		}
	}
}
