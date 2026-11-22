package app

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"mihomo-tray/internal/config"
	"mihomo-tray/internal/core"
	"mihomo-tray/internal/webui"
)

const defaultErrorMessage = "操作未能完成，详情请查看日志"

var (
	errKernelNotReady = errors.New("内核尚未就绪")
	errSystemBusy     = errors.New("系统繁忙，请稍候重试")
	errTunDeviceBusy  = errors.New("虚拟网卡已被其他程序占用")
	errNoProxyPort    = errors.New("未检测到有效代理端口")
	errInvalidPort    = errors.New("端口超出合法范围 (0-65535)")
)

var userVisibleErrors = []error{
	errKernelNotReady,
	errSystemBusy,
	errTunDeviceBusy,
	errNoProxyPort,
	errInvalidPort,
	errProfileTooLarge,
	core.ErrValidationTimeout,
	config.ErrProfileLimitExceeded,
	config.ErrProfileNameConflict,
	config.ErrProfilePathEmpty,
	config.ErrProfileNotFound,
	config.ErrProfileEmpty,
	config.ErrRemoteHTML,
	webui.ErrCacheBusy,
	webui.ErrPanelBusy,
	webui.ErrWebView2Unavailable,
	webui.ErrInvalidBrowserPath,
	webui.ErrBrowserNotFound,
}

func userFacingMessage(err error) string {
	if err == nil {
		return ""
	}

	var cfgErr *core.ConfigError
	if errors.As(err, &cfgErr) {
		return cfgErr.Error()
	}
	var apiErr *core.APIError
	if errors.As(err, &apiErr) {
		return apiErr.Error()
	}
	var httpErr *config.RemoteStatusError
	if errors.As(err, &httpErr) {
		return httpErr.Error()
	}

	for _, target := range userVisibleErrors {
		if errors.Is(err, target) {
			return target.Error()
		}
	}

	return defaultErrorMessage
}

func (a *Application) showNotice(title, message string) {
	if a.ui != nil && !a.State.IsExiting() {
		a.ui.ShowNotification(title, message)
	}
}

func (a *Application) showError(title, message string) {
	if a.ui != nil && !a.State.IsExiting() {
		a.ui.ShowError(title, message)
	}
}

func (a *Application) reportPanic(action string, r any) {
	title := action
	if !strings.HasSuffix(title, "失败") {
		title += "失败"
	}
	slog.Error(title, "err", fmt.Errorf("panic: %v", r))
	a.showError(title, "程序发生内部异常，详情请查看日志")
}

func (a *Application) notifyError(action string, err error) {
	if err == nil {
		return
	}

	if errors.Is(err, errKernelNotReady) || errors.Is(err, errSystemBusy) {
		a.showNotice("操作暂不可用", userFacingMessage(err))
		return
	}

	title := action
	if !strings.HasSuffix(title, "失败") {
		title += "失败"
	}

	slog.Error(title, "err", err)
	a.showError(title, userFacingMessage(err))
}

func (a *Application) notifyWarn(actionTitle string, err error) {
	if err == nil {
		return
	}

	title := actionTitle
	if !strings.HasSuffix(title, "失败") {
		title += "失败"
	}

	if !errors.Is(err, errSystemBusy) && !errors.Is(err, errInvalidPort) {
		slog.Warn(title, "err", err)
	}
	a.showNotice(title, userFacingMessage(err))
}
