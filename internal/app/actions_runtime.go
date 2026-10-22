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
			a.notifyWarn("配置加载", errors.New("当前配置存在错误，请检查文件内容"))
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
		if a.Platform.IsAdmin() {
			if !a.Platform.SetAutoStartTask(enable) {
				return errors.New("写入计划任务失败，请检查系统任务服务是否正常")
			}
			a.Cfg.Update(func(c *domain.TrayConfig) {
				b := enable
				c.General.Autostart = &b
			})
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
				return fmt.Errorf("提权调用失败: %w", err)
			}
			return nil
		}

		a.ExitForRestart()
		return nil
	})
}

func (a *Application) handleToggleRunAsAdmin(_ context.Context, cmd domain.UICommand) {
	enable := cmd.Payload == "true"
	a.asyncRun("管理员模式", func() error {
		a.Cfg.Update(func(c *domain.TrayConfig) {
			c.General.RunAsAdmin = enable
		})

		if !enable || a.Platform.IsAdmin() {
			return nil
		}

		restarted, err := a.Platform.Elevate()
		if err != nil || !restarted {
			a.Cfg.Update(func(c *domain.TrayConfig) {
				c.General.RunAsAdmin = false
			})
			if err != nil {
				return fmt.Errorf("提权调用失败: %w", err)
			}
			return nil
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
