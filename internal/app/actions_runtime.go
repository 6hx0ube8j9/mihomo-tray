package app

import (
	"context"
	"errors"

	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/state"
)

// ============================================================================
// 系统与生命周期指令分发 (Domain: Sys & Lifecycle Actions)
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
