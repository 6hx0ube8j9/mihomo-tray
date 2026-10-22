package app

import (
	"context"
	"fmt"

	"mihomo-tray/internal/domain"
)

// ============================================================================
// 系统代理与网络调优指令分发 (Domain: ProxyGuard & Tuner Actions)
// ============================================================================

func (a *Application) handleToggleProxy(_ context.Context, cmd domain.UICommand) {
	a.asyncRun("系统代理", func() error {
		a.ProxyGuard.SetProxy(cmd.Payload == "true")
		return nil
	})
}


func (a *Application) handleToggleTun(ctx context.Context, cmd domain.UICommand) {
	enable := (cmd.Payload == "true")

	a.asyncRun("TUN 模式", func() error {
		if enable && !a.Platform.IsAdmin() {
			a.Cfg.Update(func(c *domain.TrayConfig) {
				c.Config.Tun.Enable = true
			})

			restarted, err := a.Platform.Elevate()
			if err != nil || !restarted {
				a.Cfg.Update(func(c *domain.TrayConfig) {
					c.Config.Tun.Enable = false
				})

				if err != nil {
					return fmt.Errorf("提权调用失败: %w", err)
				}

				return nil
			}

			a.ExitForRestart()
			return nil
		}

		return a.KernelTuner.ToggleTun(ctx, enable)
	})
}

func (a *Application) handleSwitchMode(ctx context.Context, cmd domain.UICommand) {
	a.asyncRun("路由模式", func() error {
		return a.KernelTuner.SwitchMode(ctx, cmd.Payload)
	})
}

func (a *Application) handleToggleAllowLan(ctx context.Context, cmd domain.UICommand) {
	a.asyncRun("局域网代理", func() error {
		return a.KernelTuner.ToggleAllowLan(ctx, cmd.Payload == "true")
	})
}

func (a *Application) handleRequestEditPort(ctx context.Context, _ domain.UICommand) {
	go func() {
		if a.ui != nil {
			cMixed, cSocks, cHttp := a.KernelTuner.GetPortSnapshot()
			nMixed, nSocks, nHttp, ok := a.ui.ShowPortEditor(cMixed, cSocks, cHttp)
			if ok {
				a.asyncRun("端口设置", func() error {
					changed, err := a.KernelTuner.ApplyPortConfig(ctx, nMixed, nSocks, nHttp)
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
