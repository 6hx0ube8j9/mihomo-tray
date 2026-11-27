package app

import (
	"context"
	"path/filepath"

	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/logger"
	"mihomo-tray/internal/state"
)

// ============================================================================
// 系统与生命周期指令分发 (Domain: Sys & Lifecycle Actions)
// ============================================================================

func (a *Application) syncDiskConfigAndPrivilege() (needReopenWebUI bool) {
	prevCfg := a.Cfg.GetConfig()
	prevCtrl := prevCfg.ControllerSettings()
	prevUI := prevCfg.Config.ExternalUIName

	_ = a.Cfg.ReloadFromDisk()

	nextCfg := a.Cfg.GetConfig()
	logger.SyncLogLevel(nextCfg.General.TrayLogLevel)
	a.KernelTuner.ReconcilePrivilege()

	return prevCtrl != nextCfg.ControllerSettings() || prevUI != nextCfg.Config.ExternalUIName
}

func (a *Application) handleReloadConfig(ctx context.Context, _ domain.UICommand) {
	a.runExclusiveAction(state.ActionReload, "重载配置", func() error {
		needReopen := a.syncDiskConfigAndPrivilege()

		if err := a.KernelDeployer.ReloadConfig(ctx); err != nil {
			return err
		}

		a.ProxyGuard.Sync()
		if needReopen {
			a.WebUIService.ReopenIfActive(a.ForceSyncAPI)
		} else {
			a.ForceSyncAPI()
			a.WebUIService.ReloadIfActive()
		}
		return nil
	})
}

func (a *Application) handleRestartKernel(ctx context.Context, _ domain.UICommand) {
	a.runExclusiveAction(state.ActionRestart, "重启内核", func() error {
		needReopen := a.syncDiskConfigAndPrivilege()

		if err := a.KernelDeployer.RestartKernel(ctx); err != nil {
			return err
		}

		if needReopen {
			a.WebUIService.ReopenIfActive(a.ForceSyncAPI)
		} else {
			a.WebUIService.ReloadIfActive()
		}
		return nil
	})
}

func (a *Application) handleOpenBaseDir(_ context.Context, _ domain.UICommand) {
	a.asyncRunError("打开主目录", func() error {
		return a.Platform.OpenFolder(a.Cfg.BaseDir())
	})
}

func (a *Application) handleOpenAppConfig(_ context.Context, _ domain.UICommand) {
	a.asyncRunError("打开程序配置", func() error {
		jsonPath := filepath.Join(a.Cfg.BaseDir(), domain.TrayConfigName)
		return a.Platform.OpenFile(jsonPath)
	})
}

func (a *Application) handleToggleAutoStart(_ context.Context, cmd domain.UICommand) {
	enable := cmd.Payload == "true"
	if a.Platform.IsAdmin() {
		a.asyncRunError("开机自启", func() error {
			if err := a.Platform.SetAutoStartTask(enable); err != nil {
				return err
			}
			a.Cfg.Update(func(c *domain.TrayConfig) {
				b := enable
				c.General.Autostart = &b
			})
			return nil
		})
		return
	}

	a.runExclusiveAction(state.ActionRestart, "开机自启", func() error {
		original := a.Cfg.GetConfig().IsAutostartEnabled()
		if original == enable {
			return nil
		}

		return a.elevateAndRestart(func() {
			a.Cfg.Update(func(c *domain.TrayConfig) {
				b := enable
				c.General.Autostart = &b
			})
		}, func() {
			a.Cfg.Update(func(c *domain.TrayConfig) {
				b := original
				c.General.Autostart = &b
			})
		})
	})
}

func (a *Application) handleToggleRunAsAdmin(_ context.Context, cmd domain.UICommand) {
	enable := cmd.Payload == "true"
	if !enable || a.Platform.IsAdmin() {
		a.asyncRunError("管理员模式", func() error {
			a.Cfg.Update(func(c *domain.TrayConfig) {
				c.General.RunAsAdmin = enable
			})
			return nil
		})
		return
	}

	a.runExclusiveAction(state.ActionRestart, "管理员模式", func() error {
		if a.Cfg.GetConfig().General.RunAsAdmin {
			return nil
		}

		return a.elevateAndRestart(func() {
			a.Cfg.Update(func(c *domain.TrayConfig) {
				c.General.RunAsAdmin = true
			})
		}, func() {
			a.Cfg.Update(func(c *domain.TrayConfig) {
				c.General.RunAsAdmin = false
			})
		})
	})
}

func (a *Application) handleExitApp(_ context.Context, _ domain.UICommand) {
	if a.ui != nil {
		a.ui.Exit()
	}
}
