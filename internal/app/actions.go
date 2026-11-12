package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/state"
)

// ============================================================================
// 1. 核心调度基础设施 (Infrastructure, Runners & Notifiers)
// ============================================================================

func (a *Application) notifyError(action string, err error) {
	if err == nil {
		return
	}

	title := action
	if errors.Is(err, domain.ErrKernelNotReady) || errors.Is(err, domain.ErrSystemBusy) {
		title = "操作失败"
	} else if !strings.HasSuffix(title, "失败") {
		title += "失败"
	}

	slog.Error(title, "action", action, "err", err)
	if a.ui != nil && !a.State.IsExiting() {
		a.ui.ShowError(title, err.Error())
	}
}

func (a *Application) notifyWarn(actionTitle string, err error) {
	if err == nil {
		return
	}

	title := actionTitle
	if !strings.HasSuffix(title, "失败") {
		title += "失败"
	}

	slog.Warn(title, "action", actionTitle, "err", err)
	if a.ui != nil && !a.State.IsExiting() {
		a.ui.ShowNotification(title, err.Error())
	}
}

func (a *Application) notifySuccess(title, message string) {
	slog.Info(title, "detail", message)
	if a.ui != nil && !a.State.IsExiting() {
		a.ui.ShowNotification(title, message)
	}
}

func (a *Application) runExclusiveAction(action state.KernelAction, title string, task func() error) {
	if !a.State.TryBeginAction(action) {
		a.notifyWarn(title, domain.ErrSystemBusy)
		return
	}

	a.Presenter.ForcePushUIState()

	go func() {
		defer func() {
			a.State.EndAction()
			a.Presenter.ForcePushUIState()

			if r := recover(); r != nil {
				slog.Error("独占任务异常", "action", title, "panic", r)
				a.notifyError(title, fmt.Errorf("运行时异常: %v", r))
			}
		}()

		if err := task(); err != nil {
			a.notifyError(title, err)
		}
	}()
}

func (a *Application) asyncRun(title string, task func() error) {
	a.runAsync(title, a.notifyWarn, task)
}

func (a *Application) asyncRunError(title string, task func() error) {
	a.runAsync(title, a.notifyError, task)
}

func (a *Application) runAsync(title string, onErr func(string, error), task func() error) {
	go func() {
		defer a.Presenter.ForcePushUIState()

		defer func() {
			if r := recover(); r != nil {
				slog.Error("异步任务异常", "action", title, "panic", r)
				onErr(title, fmt.Errorf("运行时异常: %v", r))
			}
		}()

		if err := task(); err != nil {
			onErr(title, err)
		}
	}()
}

func (a *Application) elevateAndRestart(title string, commit func(), rollback func()) error {
	commit()

	restarted, err := a.Platform.Elevate()
	if err != nil || !restarted {
		rollback()
		if err != nil {
			slog.Debug("提权调用失败或被取消", "action", title, "err", err)
		}
		return nil
	}

	a.ExitForRestart()
	return nil
}

// ============================================================================
// 2. 指令路由中心 (Command Dispatcher Router)
// ============================================================================

func (a *Application) handleUICommand(ctx context.Context, cmd domain.UICommand) {
	switch cmd.Action {

	// --- 订阅与配置管理 (Domain: ProfileHub in actions_profile.go) ---
	case domain.ActionOpenProfileManager:  a.handleOpenProfileManager(ctx, cmd)
	case domain.ActionRequestAddLocal:     a.handleRequestAddLocal(ctx, cmd)
	case domain.ActionRequestAddRemote:    a.handleRequestAddRemote(ctx, cmd)
	case domain.ActionEditProfileInfo:     a.handleEditProfileInfo(ctx, cmd)
	case domain.ActionUpdateRemoteProfile: a.handleUpdateRemoteProfile(ctx, cmd)
	case domain.ActionSetProfileInterval:  a.handleSetProfileInterval(ctx, cmd)
	case domain.ActionSwitchProfile:       a.handleSwitchProfile(ctx, cmd)
	case domain.ActionRemoveProfile:       a.handleRemoveProfile(ctx, cmd)
	case domain.ActionMoveProfileUp:       a.handleMoveProfileUp(ctx, cmd)
	case domain.ActionMoveProfileDown:     a.handleMoveProfileDown(ctx, cmd)
	case domain.ActionOpenConfigFile:      a.handleOpenConfigFile(ctx, cmd)
	case domain.ActionEditCurrentConfig:   a.handleEditCurrentConfig(ctx, cmd)

	// --- 系统代理与网络调优 (Domain: ProxyGuard & Tuner in actions_network.go) ---
	case domain.ActionToggleProxy:         a.handleToggleProxy(ctx, cmd)
	case domain.ActionToggleTun:           a.handleToggleTun(ctx, cmd)
	case domain.ActionSwitchMode:          a.handleSwitchMode(ctx, cmd)
	case domain.ActionToggleAllowLan:      a.handleToggleAllowLan(ctx, cmd)
	case domain.ActionRequestEditPort:     a.handleRequestEditPort(ctx, cmd)

	// --- Web 面板管理 (Domain: Panel in actions_webui.go) ---
	case domain.ActionOpenWebUI:             a.handleOpenWebUI(ctx, cmd)
	case domain.ActionRequestEditController: a.handleRequestEditController(ctx, cmd)
	case domain.ActionSetWebOpenMode:        a.handleSetWebOpenMode(ctx, cmd)
	case domain.ActionToggleRemoteWebUI:     a.handleToggleRemoteWebUI(ctx, cmd)
	case domain.ActionCopyWebUIPassword:     a.handleCopyWebUIPassword(ctx, cmd)
	case domain.ActionClearWebUICache:       a.handleClearWebUICache(ctx, cmd)

	// --- 系统与生命周期 (Domain: Sys & Lifecycle in actions_runtime.go) ---
	case domain.ActionReloadConfig:        a.handleReloadConfig(ctx, cmd)
	case domain.ActionRestartKernel:       a.handleRestartKernel(ctx, cmd)
	case domain.ActionOpenBaseDir:         a.handleOpenBaseDir(ctx, cmd)
	case domain.ActionOpenAppConfig:       a.handleOpenAppConfig(ctx, cmd)
	case domain.ActionToggleAutoStart:     a.handleToggleAutoStart(ctx, cmd)
	case domain.ActionToggleRunAsAdmin:    a.handleToggleRunAsAdmin(ctx, cmd)
	case domain.ActionExitApp:             a.handleExitApp(ctx, cmd)

	default:
		slog.Warn("未识别的 UI 指令", "action", cmd.Action)
	}
}