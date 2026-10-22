package app

import (
	"context"
	"errors"
	"path/filepath"

	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/state"
	"mihomo-tray/internal/sys"
)

// ============================================================================
// 系统与生命周期指令分发 (Domain: Sys & Lifecycle Actions)
// ============================================================================

func (a *Application) handleReloadConfig(ctx context.Context, _ domain.UICommand) {
	a.runExclusiveAction(state.ActionReload, "重载配置", func() error {
		_ = a.Cfg.ReloadFromDisk()
		a.KernelTuner.ReconcilePrivilege()

		if err := a.KernelDeployer.ReloadConfig(ctx); err != nil {
			return err
		}

		a.ProxyGuard.Sync()
		a.WebUIService.ReopenIfActive(a.ForceSyncAPI)
		return nil
	})
}

func (a *Application) handleRestartKernel(ctx context.Context, _ domain.UICommand) {
	a.runExclusiveAction(state.ActionRestart, "重启内核", func() error {
		_ = a.Cfg.ReloadFromDisk()
		a.KernelTuner.ReconcilePrivilege()

		if err := a.KernelDeployer.RestartKernel(); err != nil {
			a.FallbackToEmpty(ctx, "重启配置校验失败")
			a.notifyWarn("重启内核", errors.New("配置校验失败，已自动回退至空配置运行"))
			return nil
		}

		a.ProxyGuard.Sync()
		a.WebUIService.ReopenIfActive(a.ForceSyncAPI)
		return nil
	})
}

func (a *Application) handleOpenBaseDir(_ context.Context, _ domain.UICommand) {
	a.asyncRun("打开主目录", func() error {
		return a.Platform.OpenFolder(a.Cfg.BaseDir())
	})
}

func (a *Application) handleOpenAppConfig(_ context.Context, _ domain.UICommand) {
	a.asyncRun("打开程序配置", func() error {
		jsonPath := filepath.Join(a.Cfg.BaseDir(), domain.TrayConfigName)
		return a.Platform.OpenFile(jsonPath)
	})
}

func (a *Application) handleToggleAutoStart(_ context.Context, cmd domain.UICommand) {
	enable := cmd.Payload == "true"
	a.asyncRun("开机自启", func() error {
		if sys.IsAdmin() {
			if a.Platform.SetAutoStartTask(enable) {
				a.Cfg.Update(func(c *domain.TrayConfig) {
					b := enable
					c.General.Autostart = &b
				})
			}
			return nil
		}

		a.Cfg.Update(func(c *domain.TrayConfig) {
			b := enable
			c.General.Autostart = &b
		})

		restarted, err := a.Platform.Elevate()
		if err != nil || !restarted {
			a.Cfg.Update(func(c *domain.TrayConfig) {
				b := !enable
				c.General.Autostart = &b
			})
			if err != nil {
				return err
			}
			return errors.New("已取消提权操作")
		}

		a.ExitForRestart()
		return nil
	})
}

func (a *Application) handleToggleRunAsAdmin(_ context.Context, cmd domain.UICommand) {
	enable := cmd.Payload == "true"
	a.asyncRun("以管理员运行", func() error {
		a.Cfg.Update(func(c *domain.TrayConfig) {
			c.General.RunAsAdmin = enable
		})

		if !enable {
			return nil
		}

		restarted, err := a.Platform.Elevate()
		if err != nil || !restarted {
			a.Cfg.Update(func(c *domain.TrayConfig) {
				c.General.RunAsAdmin = false
			})
			if err != nil {
				return err
			}
			return errors.New("已取消提权操作")
		}

		a.ExitForRestart()
		return nil
	})
}

func (a *Application) handleExitApp(_ context.Context, _ domain.UICommand) {
	if a.ui != nil {
		a.ui.Exit()
	}
}
