package app

import (
	"context"
	"errors"
	"log/slog"

	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/state"
)

// ============================================================================
// Web 面板管理指令分发 (Domain: Panel Actions)
// ============================================================================

func (a *Application) handleOpenWebUI(_ context.Context, cmd domain.UICommand) {
	go func() {
		if a.ui == nil {
			return
		}

		if err := a.WebUIService.Open(a.ForceSyncAPI, cmd.OnDone); err != nil {
			a.ui.ShowError("提示", err.Error())
			return
		}
	}()
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
		cRemoteURL := a.Cfg.GetEffectiveRemoteWebUIURL()
		
		cSys := a.Cfg.GetWebOpenMode() == domain.WebOpenModeSystem

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
				bOnline, bRemoteURL := nOnline, nRemoteURL
				c.General.RemoteWebUI = &bOnline
				c.General.RemoteWebUIURL = &bRemoteURL
				
				if nSys {
					m := domain.WebOpenModeSystem
					c.General.WebOpenMode = &m
				} else if c.General.WebOpenMode != nil && *c.General.WebOpenMode == domain.WebOpenModeSystem {
					m := domain.DefaultWebOpenMode
					c.General.WebOpenMode = &m
				}
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

func (a *Application) handleSetWebOpenMode(_ context.Context, cmd domain.UICommand) {
	targetMode := cmd.Payload 
	a.Cfg.Update(func(c *domain.TrayConfig) {
		c.General.WebOpenMode = &targetMode
	})
	a.Presenter.PushUIState()
}

func (a *Application) handleToggleRemoteWebUI(_ context.Context, cmd domain.UICommand) {
	b := (cmd.Payload == "true")
	a.Cfg.Update(func(c *domain.TrayConfig) {
		c.General.RemoteWebUI = &b
	})
	a.Presenter.PushUIState()
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
			a.ui.ShowError("操作失败", err.Error())
			return
		}

		a.notifySuccess("清理完成", "面板缓存已清空")
	}()
}
