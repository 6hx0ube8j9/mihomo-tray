//go:build windows

package sys

import (
	"context"
	"log/slog"

	"golang.org/x/sys/windows/registry"
)

const (
	themeSettingsPath = `Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`
	themeKeyApp       = "AppsUseLightTheme"
)

func IsDarkMode() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, themeSettingsPath, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()

	val, _, err := k.GetIntegerValue(themeKeyApp)
	if err != nil {
		return false
	}

	return val == 0
}

func WatchSystemTheme(ctx context.Context, themeCh chan<- bool) {
	lastIsDark := IsDarkMode()

	err := watchRegistryKey(ctx, themeSettingsPath, func() {
		currentIsDark := IsDarkMode()
		if currentIsDark == lastIsDark {
			return
		}
		lastIsDark = currentIsDark
		select {
		case themeCh <- currentIsDark:
		case <-ctx.Done():
		}
	})
	if err != nil && ctx.Err() == nil {
		slog.Warn("主题注册表监听失败", "err", err)
	}
}