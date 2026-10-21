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

func (a *Application) notifyError(actionTitle string, err error) {
	if err == nil {
		return
	}
	title := actionTitle
	if !strings.HasSuffix(title, "失败") {
		title += "失败"
	}

	slog.Error(title, "err", err)
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

	slog.Warn(title, "err", err)
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
		if a.ui != nil && !a.State.IsExiting() {
			a.ui.ShowNotification(title, "系统繁忙，正在处理其他任务，请稍后重试")
		}
		return
	}

	a.Presenter.ForcePushUIState()

	go func() {
		defer func() {
			a.State.EndAction()
			a.Presenter.ForcePushUIState()

			if r := recover(); r != nil {
				slog.Error("独占任务 Panic 崩溃拦截", "action", title, "panic", r)
				a.notifyError(title, fmt.Errorf("运行时异常: %v", r))
			}
		}()

		if err := task(); err != nil {
			a.notifyError(title, err)
		}
	}()
}

func (a *Application) asyncRun(title string, task func() error) {
	go func() {
		defer a.Presenter.ForcePushUIState()

		defer func() {
			if r := recover(); r != nil {
				slog.Error("异步任务 Panic 崩溃拦截", "title", title, "panic", r)
				a.notifyWarn(title, fmt.Errorf("运行时异常: %v", r))
			}
		}()

		if err := task(); err != nil {
			a.notifyWarn(title, err)
		}
	}()
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

	// --- 系统代理控制 (Domain: ProxyGuard) ---
	case domain.ActionToggleProxy:         a.handleToggleProxy(ctx, cmd)

	// --- 运行参数调优 (Domain: Tuner) ---
	case domain.ActionToggleTun:           a.handleToggleTun(ctx, cmd)
	case domain.ActionSwitchMode:          a.handleSwitchMode(ctx, cmd)
	case domain.ActionToggleAllowLan:      a.handleToggleAllowLan(ctx, cmd)
	case domain.ActionRequestEditPort:     a.handleRequestEditPort(ctx, cmd)

	// --- Web 面板管理 (Domain: Panel in actions_panel.go) ---
	case domain.ActionOpenWebUI:             a.handleOpenWebUI(ctx, cmd)
	case domain.ActionRequestEditController: a.handleRequestEditController(ctx, cmd)
	case domain.ActionToggleSystemBrowser:   a.handleToggleSystemBrowser(ctx, cmd)
	case domain.ActionToggleRemoteWebUI:     a.handleToggleRemoteWebUI(ctx, cmd)
	case domain.ActionCopyWebUIPassword:     a.handleCopyWebUIPassword(ctx, cmd)
	case domain.ActionClearWebUICache:       a.handleClearWebUICache(ctx, cmd)

	// --- 系统与生命周期 (Domain: Sys & Lifecycle) ---
	case domain.ActionReloadConfig:        a.handleReloadConfig(ctx, cmd)
	case domain.ActionRestartKernel:       a.handleRestartKernel(ctx, cmd)
	case domain.ActionOpenBaseDir:         a.handleOpenBaseDir(ctx, cmd)
	case domain.ActionOpenAppConfig:       a.handleOpenAppConfig(ctx, cmd)
	case domain.ActionOpenConfigFile:      a.handleOpenConfigFile(ctx, cmd)
	case domain.ActionEditCurrentConfig:   a.handleEditCurrentConfig(ctx, cmd)
	case domain.ActionToggleAutoStart:     a.handleToggleAutoStart(ctx, cmd)
	case domain.ActionToggleRunAsAdmin:    a.handleToggleRunAsAdmin(ctx, cmd)
	case domain.ActionExitApp:             a.handleExitApp(ctx, cmd)

	default:
		slog.Warn("未识别的 UI 指令", "action", cmd.Action)
	}
}

// ============================================================================
// 3. 系统代理与核心参数调优 (Domain: ProxyGuard & Tuner)
// ============================================================================

func (a *Application) handleToggleProxy(_ context.Context, cmd domain.UICommand) {
	a.asyncRun("系统代理", func() error {
		a.ProxyGuard.SetProxy(cmd.Payload == "true")
		return nil
	})
}

func (a *Application) handleToggleTun(ctx context.Context, cmd domain.UICommand) {
	a.asyncRun("TUN 模式", func() error {
		restarted, err := a.Tuner.ToggleTun(ctx, cmd.Payload == "true", a.HostBridge.ElevatePrivilege)
		if err == nil && restarted {
			a.ExitForRestart()
		}
		return err
	})
}

func (a *Application) handleSwitchMode(ctx context.Context, cmd domain.UICommand) {
	a.asyncRun("路由模式", func() error {
		return a.Tuner.SwitchMode(ctx, cmd.Payload)
	})
}

func (a *Application) handleToggleAllowLan(ctx context.Context, cmd domain.UICommand) {
	a.asyncRun("局域网代理", func() error {
		return a.Tuner.ToggleAllowLan(ctx, cmd.Payload == "true")
	})
}

func (a *Application) handleRequestEditPort(ctx context.Context, _ domain.UICommand) {
	go func() {
		if a.ui != nil {
			cMixed, cSocks, cHttp := a.Tuner.GetPortSnapshot()
			nMixed, nSocks, nHttp, ok := a.ui.ShowPortEditor(cMixed, cSocks, cHttp)
			if ok {
				a.asyncRun("端口设置", func() error {
					changed, err := a.Tuner.ApplyPortConfig(ctx, nMixed, nSocks, nHttp)
					if err != nil {
						return err
					}
					if changed && a.Cfg.IsSystemProxyEnabled() {
						a.ProxyGuard.Sync()
					}
					return nil
				})
			}
		}
	}()
}

// ============================================================================
// 4. 系统与生命周期 (Domain: Sys & Lifecycle)
// ============================================================================

func (a *Application) handleReloadConfig(ctx context.Context, _ domain.UICommand) {
	a.runExclusiveAction(state.ActionReload, "重载配置", func() error {
		_ = a.Cfg.ReloadFromDisk()
		a.HostBridge.ReconcileRuntimePrivileges()

		if err := a.Deployer.ReloadConfig(ctx); err != nil {
			return err
		}

		a.ProxyGuard.Sync()
		a.Panel.ReopenIfActive(a.ForceSyncAPI)
		return nil
	})
}

func (a *Application) handleRestartKernel(ctx context.Context, _ domain.UICommand) {
	a.runExclusiveAction(state.ActionRestart, "重启内核", func() error {
		_ = a.Cfg.ReloadFromDisk()
		a.HostBridge.ReconcileRuntimePrivileges()

		if err := a.Deployer.RestartKernel(); err != nil {
			a.FallbackToEmpty(ctx, "重启配置校验失败")
			a.notifyWarn("重启内核", errors.New("配置校验失败，已自动回退至空配置运行"))
			return nil
		}

		a.ProxyGuard.Sync()
		a.Panel.ReopenIfActive(a.ForceSyncAPI)
		return nil
	})
}

func (a *Application) handleOpenBaseDir(_ context.Context, _ domain.UICommand) {
	a.HostBridge.OpenBaseDir()
}

func (a *Application) handleOpenAppConfig(_ context.Context, _ domain.UICommand) {
	a.HostBridge.OpenAppConfig()
}

func (a *Application) handleOpenConfigFile(_ context.Context, cmd domain.UICommand) {
	a.asyncRun("打开配置", func() error {
		return a.HostBridge.OpenConfigFile(cmd.Payload)
	})
}

func (a *Application) handleEditCurrentConfig(_ context.Context, _ domain.UICommand) {
	a.asyncRun("编辑配置", func() error {
		return a.HostBridge.EditCurrentConfig()
	})
}

func (a *Application) handleToggleAutoStart(_ context.Context, cmd domain.UICommand) {
	a.asyncRun("开机自启", func() error {
		if a.HostBridge.ToggleAutoStart(cmd.Payload == "true") {
			a.ExitForRestart()
		}
		return nil
	})
}

func (a *Application) handleToggleRunAsAdmin(_ context.Context, cmd domain.UICommand) {
	a.asyncRun("以管理员运行", func() error {
		if a.HostBridge.ToggleRunAsAdmin(cmd.Payload == "true") {
			a.ExitForRestart()
		}
		return nil
	})
}

func (a *Application) handleExitApp(_ context.Context, _ domain.UICommand) {
	if a.ui != nil {
		a.ui.Exit()
	}
}
