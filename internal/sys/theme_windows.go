package sys

import (
	"context"
	"log/slog"
	"runtime"

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
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	k, err := registry.OpenKey(registry.CURRENT_USER, themeSettingsPath, registry.NOTIFY|registry.QUERY_VALUE)
	if err != nil {
		slog.Warn("打开主题注册表监听失败", "err", err)
		return
	}
	defer k.Close()

	notifyEvent, err := windows.CreateEvent(nil, 0, 0, nil)
	if err != nil {
		slog.Error("创建主题通知事件失败", "err", err)
		return
	}
	defer windows.CloseHandle(notifyEvent)

	cancelEvent, err := windows.CreateEvent(nil, 0, 0, nil)
	if err != nil {
		slog.Error("创建取消事件失败", "err", err)
		return
	}
	defer windows.CloseHandle(cancelEvent)

	stopCancel := context.AfterFunc(ctx, func() {
		_ = windows.SetEvent(cancelEvent)
	})
	defer stopCancel()

	arm := func() {
		filter := uint32(windows.REG_NOTIFY_CHANGE_LAST_SET | windows.REG_NOTIFY_CHANGE_NAME | windows.REG_NOTIFY_THREAD_AGNOSTIC)
		if err := windows.RegNotifyChangeKeyValue(windows.Handle(k), false, filter, notifyEvent, true); err != nil {
			filter &^= windows.REG_NOTIFY_THREAD_AGNOSTIC
			_ = windows.RegNotifyChangeKeyValue(windows.Handle(k), false, filter, notifyEvent, true)
		}
	}

	arm()

	handles := [2]windows.Handle{notifyEvent, cancelEvent}

	const debounceMs = 150
	waitTimeout := uint32(windows.INFINITE)
	hasPendingUpdate := false
	
	lastTheme := GetCurrentTheme()

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

				currentTheme := GetCurrentTheme()
				if currentTheme != lastTheme {
					lastTheme = currentTheme

					select {
					case themeCh <- currentTheme:
					case <-ctx.Done():
						return
					}
				}
			}
		}
	}
}
