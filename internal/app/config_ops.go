package app

import (
	"context"
	"log/slog"
	"time"

	"mihomo-tray/internal/domain"
)

func (a *Application) ApplyPortConfig(ctx context.Context, mixed, socks, httpPort int) {
	a.Cfg.Update(func(c *domain.TrayConfig) {
		m, s, h := mixed, socks, httpPort
		c.Config.MixedPort = &m
		c.Config.SocksPort = &s
		c.Config.Port = &h
	})
	a.pushUIState()

	if *a.Cfg.GetConfig().General.SystemProxy {
		a.syncSystemProxy()
	}

	a.State.SetConfigSyncing(true)
	defer a.State.SetConfigSyncing(false)

	if a.State.GetPhase() == domain.PhaseRunning {
		reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		payload := map[string]interface{}{"mixed-port": mixed, "socks-port": socks, "port": httpPort}
		if err := a.API.SyncConfigToKernel(reqCtx, payload); err != nil {
			slog.Warn("热刷端口到内核失败，等待下次内核重载生效", "err", err)
		}
	}
	select { case a.apiPollCh <- struct{}{}: default: }
}

func (a *Application) ApplyControllerConfig(addr, secret string, online, sysBrowser bool, coreChanged bool) {
	a.Cfg.Update(func(c *domain.TrayConfig) {
		if coreChanged {
			c.Config.ExternalController = addr
			c.Config.Secret = &secret
		}
		bOnline, bSys := online, sysBrowser
		c.General.RemoteWebUI = &bOnline
		c.General.SystemBrowser = &bSys
	})

	a.pushUIState()

	if coreChanged {
		slog.Info("Web 面板核心网络参数已变更，重启内核生效")
		a.RestartKernel()
	}
}

func (a *Application) ToggleTun(ctx context.Context, enable bool) (restarted bool) {
	a.Cfg.Update(func(c *domain.TrayConfig) { c.Config.Tun.Enable = enable })
	
	restarted = a.ElevatePrivilege(func() {
		a.Cfg.Update(func(c *domain.TrayConfig) { c.Config.Tun.Enable = false })
	})
	if restarted { return true }

	if enable {
		a.State.SetTunRequestedTime(time.Now())
	}

	a.State.SetConfigSyncing(true)
	defer a.State.SetConfigSyncing(false)

	tunPayload := map[string]interface{}{"enable": enable}
	if dev := a.State.GetActualTunDevice(); dev != "" {
		tunPayload["device"] = dev
	}
	
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := a.API.SyncConfigToKernel(reqCtx, map[string]interface{}{"tun": tunPayload}); err != nil {
		a.Cfg.Update(func(c *domain.TrayConfig) { c.Config.Tun.Enable = !enable })
	}
	select { case a.apiPollCh <- struct{}{}: default: }
	return false
}

func (a *Application) ToggleProxy(enable bool) {
	a.Cfg.Update(func(c *domain.TrayConfig) {
		b := enable
		c.General.SystemProxy = &b
	})
	a.syncSystemProxy()
}

func (a *Application) SwitchMode(ctx context.Context, mode string) {
	a.Cfg.Update(func(c *domain.TrayConfig) { c.Config.Mode = mode })
	a.State.SetConfigSyncing(true)
	defer a.State.SetConfigSyncing(false)
	
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_ = a.API.SyncConfigToKernel(reqCtx, map[string]interface{}{"mode": mode})
	select { case a.apiPollCh <- struct{}{}: default: }
}

func (a *Application) ToggleAllowLan(ctx context.Context, enable bool) {
	a.Cfg.Update(func(c *domain.TrayConfig) {
		b := enable
		c.Config.AllowLan = &b
	})
	a.State.SetConfigSyncing(true)
	defer a.State.SetConfigSyncing(false)
	
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_ = a.API.SyncConfigToKernel(reqCtx, map[string]interface{}{"allow-lan": enable})
	select { case a.apiPollCh <- struct{}{}: default: }
}
