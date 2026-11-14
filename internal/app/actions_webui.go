package app

import (
	"context"
	"errors"

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

func (a *Application) handleRequestEditController(_ context.Context, _ domain.UICommand) {
	go func() {
		if a.ui == nil {
			return
		}

		cur := a.Cfg.GetControllerSettings()
		next, ok := a.ui.ShowControllerEditor(cur)
		if !ok {
			return
		}

		a.runExclusiveAction(state.ActionRestart, "面板设置", func() error {
			coreChanged := (cur.Addr != next.Addr) || (cur.Secret != next.Secret)
			appChanged := (cur.RemoteWebUI != next.RemoteWebUI) ||
				(cur.RemoteWebUIURL != next.RemoteWebUIURL) ||
				(cur.WebOpenMode != next.WebOpenMode) ||
				(cur.CustomBrowserPath != next.CustomBrowserPath) ||
				(cur.CustomUserDataDir != next.CustomUserDataDir)

			if !coreChanged && !appChanged {
				return nil
			}

			a.Cfg.Update(func(c *domain.TrayConfig) {
				if coreChanged {
					c.Config.ExternalController = next.Addr
					sec := next.Secret
					c.Config.Secret = &sec
				}

				online := next.RemoteWebUI
				remoteURL := next.RemoteWebUIURL
				openMode := domain.NormalizeWebOpenMode(next.WebOpenMode)
				customPath := next.CustomBrowserPath
				customDir := next.CustomUserDataDir

				c.General.RemoteWebUI = &online
				c.General.RemoteWebUIURL = &remoteURL
				c.General.WebOpenMode = &openMode
				c.General.CustomBrowserPath = &customPath
				c.General.CustomUserDataDir = &customDir
			})

			if coreChanged {
				if err := a.KernelDeployer.RestartKernel(); err != nil {
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
	if a.Cfg.GetWebOpenMode() == targetMode {
		return
	}

	a.Cfg.Update(func(c *domain.TrayConfig) {
		c.General.WebOpenMode = &targetMode
	})
	a.Presenter.PushUIState()
	a.WebUIService.ReopenIfActive(a.ForceSyncAPI)
}

func (a *Application) handleToggleRemoteWebUI(_ context.Context, cmd domain.UICommand) {
	b := (cmd.Payload == "true")
	if a.Cfg.IsRemoteWebUIEnabled() == b {
		return
	}

	a.Cfg.Update(func(c *domain.TrayConfig) {
		c.General.RemoteWebUI = &b
	})
	a.Presenter.PushUIState()
	a.WebUIService.ReopenIfActive(a.ForceSyncAPI)
}

func (a *Application) handleCopyWebUIPassword(_ context.Context, _ domain.UICommand) {
	cfg := a.Cfg.GetConfig()
	secret := a.Cfg.GetEffectiveSecret(cfg.Config.Secret)
	if secret == "" {
		a.notifyWarn("复制密码", errors.New("当前未设置面板密码"))
		return
	}

	if err := a.Platform.WriteClipboard(secret); err != nil {
		a.notifyWarn("写入剪贴板", err)
		return
	}

	a.notifySuccess("复制成功", "密码已复制到剪贴板")
}

func (a *Application) handleClearWebUICache(_ context.Context, _ domain.UICommand) {
	go func() {
		if a.ui == nil {
			return
		}

		if !a.ui.ShowConfirm("清理缓存", "确定清空面板本地缓存？") {
			return
		}

		if err := a.WebUIService.ClearCache(); err != nil {
			a.notifyError("清理缓存", err)
			return
		}

		a.notifySuccess("清理完成", "面板缓存已清空")
	}()
}