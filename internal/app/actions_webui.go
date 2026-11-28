package app

import (
	"context"

	"mihomo-tray/internal/browser"
	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/state"
)
// ============================================================================
// Web 面板管理指令分发 (Domain: Panel Actions)
// ============================================================================

func (a *Application) handleOpenWebUI(_ context.Context, cmd domain.UICommand) {
	a.asyncRunError("打开面板", func() error {
		return a.WebUIService.Open(a.ForceSyncAPI, cmd.OnDone)
	})
}

func (a *Application) handleRequestEditController(ctx context.Context, _ domain.UICommand) {
	go func() {
		if a.ui == nil {
			return
		}

		initial := a.Cfg.GetConfig().ControllerSettings()
		next, ok := a.ui.ShowControllerEditor(initial)
		if !ok {
			return
		}

		next.WebOpenMode = domain.NormalizeWebOpenMode(next.WebOpenMode)
		next.CustomBrowserPath = browser.ToPortablePath(next.CustomBrowserPath, a.Cfg.BaseDir())

		applyAppPrefs := func(c *domain.TrayConfig) {
			online := next.RemoteWebUI
			remoteURL := next.RemoteWebUIURL
			openMode := next.WebOpenMode
			customEnabled := next.CustomBrowserEnabled
			customPath := next.CustomBrowserPath

			c.General.RemoteWebUI = &online
			c.General.RemoteWebUIURL = &remoteURL
			c.General.WebOpenMode = &openMode
			c.General.CustomBrowserEnabled = &customEnabled
			c.General.CustomBrowserPath = &customPath
		}

		cur := a.Cfg.GetConfig().ControllerSettings()
		coreChanged := (cur.Addr != next.Addr) || (cur.Secret != next.Secret)
		appChanged := (cur.RemoteWebUI != next.RemoteWebUI) ||
			(cur.RemoteWebUIURL != next.RemoteWebUIURL) ||
			(cur.WebOpenMode != next.WebOpenMode) ||
			(cur.CustomBrowserEnabled != next.CustomBrowserEnabled) ||
			(cur.CustomBrowserPath != next.CustomBrowserPath)

		if !coreChanged && !appChanged {
			return
		}

		if !coreChanged {
			a.Cfg.Update(applyAppPrefs)
			a.Presenter.ForcePushUIState()
			a.WebUIService.ReopenIfActive(a.ForceSyncAPI)
			return
		}

		a.runExclusiveAction(state.ActionRestart, "面板设置", func() error {
			prev := a.Cfg.GetConfig().ControllerSettings()
			needRestart := (prev.Addr != next.Addr) || (prev.Secret != next.Secret)

			a.Cfg.Update(func(c *domain.TrayConfig) {
				if needRestart {
					c.Config.ExternalController = next.Addr
					sec := next.Secret
					c.Config.Secret = &sec
				}
				applyAppPrefs(c)
			})

			if needRestart {
				if err := a.KernelDeployer.RestartKernel(ctx); err != nil {
					a.Cfg.Update(func(c *domain.TrayConfig) {
						c.Config.ExternalController = prev.Addr
						sec := prev.Secret
						c.Config.Secret = &sec
					})
					return err
				}
			}

			a.WebUIService.ReopenIfActive(a.ForceSyncAPI)
			return nil
		})
	}()
}

func (a *Application) handleSetWebOpenMode(_ context.Context, cmd domain.UICommand) {
	targetMode := domain.NormalizeWebOpenMode(cmd.Payload)
	changed := a.Cfg.TryUpdate(func(c *domain.TrayConfig) bool {
		if c.EffectiveWebOpenMode() == targetMode {
			return false
		}
		c.General.WebOpenMode = &targetMode
		return true
	})
	if !changed {
		return
	}

	a.Presenter.ForcePushUIState()
	a.WebUIService.ReopenIfActive(a.ForceSyncAPI)
}

func (a *Application) handleToggleRemoteWebUI(_ context.Context, cmd domain.UICommand) {
	b := (cmd.Payload == "true")
	changed := a.Cfg.TryUpdate(func(c *domain.TrayConfig) bool {
		if c.IsRemoteWebUIEnabled() == b {
			return false
		}
		c.General.RemoteWebUI = &b
		return true
	})
	if !changed {
		return
	}

	a.Presenter.ForcePushUIState()
	a.WebUIService.ReopenIfActive(a.ForceSyncAPI)
}

func (a *Application) handleCopyWebUIPassword(_ context.Context, _ domain.UICommand) {
	secret := a.Cfg.GetConfig().EffectiveSecret()
	if secret == "" {
		a.showNotice("复制密码", "当前未设置密码")
		return
	}

	if err := a.Platform.WriteClipboard(secret); err != nil {
		a.notifyError("写入剪贴板", err)
		return
	}

	a.showNotice("复制成功", "密码已复制到剪贴板")
}

func (a *Application) handleClearWebUICache(_ context.Context, _ domain.UICommand) {
	go func() {
		if a.ui == nil {
			return
		}

		if !a.ui.ShowConfirm("清理缓存", "确定清空 Web 面板缓存？") {
			return
		}

		if err := a.WebUIService.ClearCache(); err != nil {
			a.notifyError("清理缓存", err)
			return
		}

		a.showNotice("清理完成", "Web 面板缓存已清空")
	}()
}
