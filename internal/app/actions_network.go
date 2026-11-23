package app

import (
	"context"
	"time"

	"mihomo-tray/internal/domain"
)

// ============================================================================
// 系统代理与网络调优指令分发 (Domain: ProxyGuard & Tuner Actions)
// ============================================================================

func (a *Application) handleToggleProxy(_ context.Context, cmd domain.UICommand) {
	enable := cmd.Payload == "true"
	title := "开启代理"
	if !enable {
		title = "关闭代理"
	}

	a.asyncRunError(title, func() error {
		return a.ProxyGuard.SetProxy(enable)
	})
}

func (a *Application) handleToggleTun(ctx context.Context, cmd domain.UICommand) {
	enable := cmd.Payload == "true"

	a.asyncRunError("TUN 模式", func() error {
		if enable && !a.Platform.IsAdmin() {
			if a.State.GetPhase() != domain.PhaseRunning {
				return errKernelNotReady
			}
			if a.State.IsActionBusy() {
				return errSystemBusy
			}

			targetDev := a.State.GetEffectiveTunDevice()
			if a.Platform.IsTunActive(targetDev) {
				return errTunDeviceBusy
			}

			originalTun := a.Cfg.GetConfig().Config.Tun.Enable
			return a.elevateAndRestart(func() {
				a.Cfg.Update(func(c *domain.TrayConfig) {
					c.Config.Tun.Enable = true
				})
				a.State.SetTunRequestedTime(time.Now())
			}, func() {
				a.Cfg.Update(func(c *domain.TrayConfig) {
					c.Config.Tun.Enable = originalTun
				})
				a.State.ClearTunStarting()
			})
		}

		return a.KernelTuner.ToggleTun(ctx, enable)
	})
}

func (a *Application) handleSwitchMode(ctx context.Context, cmd domain.UICommand) {
	a.asyncRunError("路由模式", func() error {
		return a.KernelTuner.SwitchMode(ctx, cmd.Payload)
	})
}

func (a *Application) handleToggleAllowLan(ctx context.Context, cmd domain.UICommand) {
	a.asyncRunError("局域网代理", func() error {
		return a.KernelTuner.ToggleAllowLan(ctx, cmd.Payload == "true")
	})
}

func (a *Application) handleRequestEditPort(ctx context.Context, _ domain.UICommand) {
	go func() {
		if a.ui == nil {
			return
		}

		cMixed, cSocks, cHttp := a.KernelTuner.GetPortSnapshot()
		nMixed, nSocks, nHttp, ok := a.ui.ShowPortEditor(cMixed, cSocks, cHttp)
		if !ok {
			return
		}

		a.asyncRun("端口设置", func() error {
			changed, err := a.KernelTuner.ApplyPortConfig(ctx, nMixed, nSocks, nHttp)
			if err != nil {
				return err
			}
			if changed && a.Cfg.GetConfig().IsSystemProxyEnabled() {
				a.ProxyGuard.Sync()
			}
			return nil
		})
	}()
}
