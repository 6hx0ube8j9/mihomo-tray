package app

import (
	"context"

	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/state"
)

// ============================================================================
// Web 面板管理指令分发 (Domain: Panel Actions)
// ============================================================================

func (a *Application) handleOpenWebUI(_ context.Context, _ domain.UICommand) {
	a.asyncRun("打开面板", func() error {
		return a.WebUIService.Open(a.ForceSyncAPI)
	})
}

func (a *Application) handleRequestEditController(_ context.Context, _ domain.UICommand) {
	go func() {
		if a.ui == nil {
			return
		}

		cfg := a.Cfg.GetConfig()
		cAddr := a.Cfg.GetEffectiveExternalController()
		cSec := a.Cfg.GetEffectiveSecret(cfg.Config.Secret)
		cOnline := a.Cfg.IsRemoteWebUIEnabled()
		cSys := a.Cfg.IsSystemBrowserEnabled()
		cRemoteURL := a.Cfg.GetEffectiveRemoteWebUIURL()

		nAddr, nSec, nOnline, nSys, nRemoteURL, ok := a.ui.ShowControllerEditor(cAddr, cSec, cOnline, cSys, cRemoteURL)
		if !ok {
			return
		}

		a.runExclusiveAction(state.ActionRestart, "面板设置", func() error {
			coreChanged := (cAddr != nAddr) || (cSec != nSec)
			appChanged := (cOnline != nOnline) || (cSys != nSys) || (cRemoteURL != nRemoteURL)

			if !coreChanged && !appChanged {
				return nil
			}

			a.Cfg.Update(func(c *domain.TrayConfig) {
				if coreChanged {
					c.Config.ExternalController = nAddr
					c.Config.Secret = &nSec
				}
				bOnline, bSys, bRemoteURL := nOnline, nSys, nRemoteURL
				c.General.RemoteWebUI = &bOnline
				c.General.SystemBrowser = &bSys
				c.General.RemoteWebUIURL = &bRemoteURL
			})

			if coreChanged {
				if err := a.KernelDeployer.RestartKernel(); err != nil {
					return err
				}
			}

			if coreChanged || appChanged {
				a.WebUIService.ReopenIfActive(a.ForceSyncAPI)
			}
			return nil
		})
	}()
}

func (a *Application) handleToggleSystemBrowser(_ context.Context, cmd domain.UICommand) {
	a.Panel.ToggleSystemBrowser(cmd.Payload == "true")
	a.Presenter.PushUIState()
}

func (a *Application) handleToggleRemoteWebUI(_ context.Context, cmd domain.UICommand) {
	a.Panel.ToggleRemoteWebUI(cmd.Payload == "true")
	a.Presenter.PushUIState()
}

func (a *Application) handleCopyWebUIPassword(_ context.Context, _ domain.UICommand) {
	if err := a.WebUIService.CopyPassword(); err != nil {
		a.notifyWarn("复制密码", err)
	} else {
		a.notifySuccess("复制成功", "密码已复制到剪贴板。")
	}
}

func (a *Application) handleClearWebUICache(_ context.Context, _ domain.UICommand) {
	go func() {
		if a.ui != nil && !a.ui.ShowConfirm("清理缓存", "将清除面板的本地设置与缓存，是否继续？") {
			return
		}
		a.asyncRun("清理缓存", func() error {
			if err := a.WebUIService.ClearCache(); err != nil {
				return err
			}
			a.notifySuccess("清理完成", "面板缓存已清除。")
			return nil
		})
	}()
}
