package app

import (
	"context"

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
	a.asyncRun("TUN 模式", func() error {
		restarted, err := a.Tuner.ToggleTun(ctx, cmd.Payload == "true", a.HostBridge.ElevatePrivilege)
		if err == nil && restarted {
			a.ExitForRestart()
		}
		return err
	})
}

func (a *Application) handleSwitchMode(ctx context.Context, cmd domain.UICommand) {
	a.asyncRun("路由模式", func() error {
		return a.Tuner.SwitchMode(ctx, cmd.Payload)
	})
}

func (a *Application) handleToggleAllowLan(ctx context.Context, cmd domain.UICommand) {
	a.asyncRun("局域网代理", func() error {
		return a.Tuner.ToggleAllowLan(ctx, cmd.Payload == "true")
	})
}

func (a *Application) handleRequestEditPort(ctx context.Context, _ domain.UICommand) {
	go func() {
		if a.ui != nil {
			cMixed, cSocks, cHttp := a.Tuner.GetPortSnapshot()
			nMixed, nSocks, nHttp, ok := a.ui.ShowPortEditor(cMixed, cSocks, cHttp)
			if ok {
				a.asyncRun("端口设置", func() error {
					changed, err := a.Tuner.ApplyPortConfig(ctx, nMixed, nSocks, nHttp)
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
