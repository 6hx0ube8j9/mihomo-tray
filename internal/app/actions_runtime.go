package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/state"
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
			a.FallbackToEmpty(ctx, "配置校验失败")
			a.notifyWarn("配置加载失败", err)
			return nil
		}

		a.WebUIService.ReopenIfActive(a.ForceSyncAPI)
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
	a.asyncRun("开机自启", func() error {
		if a.Platform.IsAdmin() {
			if !a.Platform.SetAutoStartTask(enable) {
				return errors.New("写入计划任务失败")
			}
			a.Cfg.Update(func(c *domain.TrayConfig) {
				b := enable
				c.General.Autostart = &b
			})
			return nil
		}

		return a.elevateAndRestart("开机自启", func() {
			a.Cfg.Update(func(c *domain.TrayConfig) {
				b := enable
				c.General.Autostart = &b
			})
		}, func() {
			a.Cfg.Update(func(c *domain.TrayConfig) {
				b := !enable
				c.General.Autostart = &b
			})
		})
	})
}

func (a *Application) handleToggleRunAsAdmin(_ context.Context, cmd domain.UICommand) {
	enable := cmd.Payload == "true"
	a.asyncRun("管理员模式", func() error {
		if !enable || a.Platform.IsAdmin() {
			a.Cfg.Update(func(c *domain.TrayConfig) {
				c.General.RunAsAdmin = enable
			})
			return nil
		}

		return a.elevateAndRestart("管理员模式", func() {
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
