package sys

import (
	"context"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var (
	wininet   = windows.NewLazySystemDLL("wininet.dll")
	setOption = wininet.NewProc("InternetSetOptionW")
)

const (
	INTERNET_OPTION_REFRESH          = 37
	INTERNET_OPTION_SETTINGS_CHANGED = 39
)

func RefreshWininet() {
	_, _, _ = setOption.Call(0, INTERNET_OPTION_SETTINGS_CHANGED, 0, 0)
	_, _, _ = setOption.Call(0, INTERNET_OPTION_REFRESH, 0, 0)
}

func EnableSystemProxy(portStr string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Internet Settings`, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()

	if portStr == "" {
		portStr = "7890"
	}

	_ = k.SetDWordValue("ProxyEnable", 1)
	_ = k.SetStringValue("ProxyServer", "127.0.0.1:"+portStr)
	_ = k.SetStringValue("ProxyOverride", "<local>;localhost;127.*;10.*;172.16.*;172.17.*;172.18.*;172.19.*;172.20.*;172.21.*;172.22.*;172.23.*;172.24.*;172.25.*;172.26.*;172.27.*;172.28.*;172.29.*;172.30.*;172.31.*;192.168.*")
	
	RefreshWininet()
	return nil
}

func DisableSystemProxy() error {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Internet Settings`, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()

	_ = k.SetDWordValue("ProxyEnable", 0)

	RefreshWininet()
	return nil
}

func WatchProxyRegistry(ctx context.Context, getExpectedProxy func() bool, getExpectedPort func() string, onRevert func(), onReapply func()) {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Internet Settings`, registry.NOTIFY|registry.QUERY_VALUE)
	if err != nil {
		return
	}
	defer k.Close()

	var retryCount int
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		event, err := windows.CreateEvent(nil, 0, 0, nil)
		if err != nil {
			time.Sleep(1 * time.Second)
			continue
		}

		err = windows.RegNotifyChangeKeyValue(windows.Handle(k), false, windows.REG_NOTIFY_CHANGE_LAST_SET, event, true)
		if err != nil {
			windows.CloseHandle(event)
			time.Sleep(1 * time.Second)
			continue
		}

		s, _ := windows.WaitForSingleObject(event, 2000)
		windows.CloseHandle(event)

		if s == windows.WAIT_OBJECT_0 {
			expectedProxy := getExpectedProxy()
			if !expectedProxy {
				continue
			}

			val, _, err := k.GetIntegerValue("ProxyEnable")
			realProxy := (err == nil && val == 1)

			serverStr, _, errStr := k.GetStringValue("ProxyServer")
			expectedPort := getExpectedPort()
			if expectedPort == "" {
				expectedPort = "7890"
			}
			expectedServer := "127.0.0.1:" + expectedPort
			isPortHijacked := (errStr == nil && serverStr != expectedServer && serverStr != "")

			if realProxy && isPortHijacked {
				retryCount = 0
				onRevert()
				time.Sleep(100 * time.Millisecond)
				continue
			}

			if realProxy && !isPortHijacked {
				retryCount = 0
				continue
			}

			retryCount++
			if retryCount <= 3 {
				time.Sleep(200 * time.Millisecond)
				onReapply()
			} else {
				retryCount = 0
				onRevert()
			}
		}
	}
}
