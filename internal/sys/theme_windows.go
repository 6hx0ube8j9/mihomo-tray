package sys

import (
	"context"
	"log/slog"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	themeSettingsPath = `Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`
	themeKeyApp       = "AppsUseLightTheme"
	
	ThemeDark  = "dark"
	ThemeLight = "light"
)

func GetCurrentTheme() string {
	k, err := registry.OpenKey(registry.CURRENT_USER, themeSettingsPath, registry.QUERY_VALUE)
	if err != nil {
		return ThemeLight 
	}
	defer k.Close()

	val, _, err := k.GetIntegerValue(themeKeyApp)
	if err != nil {
		return ThemeLight
	}

	if val == 0 {
		return ThemeDark
	}
	return ThemeLight
}

func WatchSystemTheme(ctx context.Context, themeCh chan<- string) {
	k, err := registry.OpenKey(registry.CURRENT_USER, themeSettingsPath, registry.NOTIFY|registry.QUERY_VALUE)
	if err != nil {
		slog.Warn("打开主题注册表监听失败", "err", err)
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

	for {
		event, err := windows.WaitForMultipleObjects(handles[:], false, windows.INFINITE)
		if err != nil || event == windows.WAIT_OBJECT_0+1 {
			return
		}

		if event == windows.WAIT_OBJECT_0 {
			arm()

			currentTheme := GetCurrentTheme()
			select {
			case themeCh <- currentTheme:
			default:
			}
		}
	}
}
