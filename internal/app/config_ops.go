package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"mihomo-tray/internal/domain"
)

func (a *Application) ToggleTun(ctx context.Context, enable bool) (restarted bool) {
	originalTun := a.Cfg.GetConfig().Config.Tun.Enable
	if originalTun == enable {
		return false
	}

	a.Cfg.Update(func(c *domain.TrayConfig) { c.Config.Tun.Enable = enable })
	if enable {
		a.State.SetTunRequestedTime(time.Now())
	}

	restarted = a.ElevatePrivilege(func() {
		a.Cfg.Update(func(c *domain.TrayConfig) { c.Config.Tun.Enable = originalTun })
		if enable {
			a.State.SetTunRequestedTime(time.Time{})
		}
	})
	if restarted {
		return true
	}

	if a.State.GetPhase() != domain.PhaseRunning {
		a.Cfg.Update(func(c *domain.TrayConfig) { c.Config.Tun.Enable = originalTun })
		if enable {
			a.State.SetTunRequestedTime(time.Time{})
		}
		if a.ui != nil {
			a.ui.ShowError("TUN 设置失败", "内核当前处于异常状态，操作已被丢弃。")
		}
		return false
	}

	a.State.SetConfigSyncing(true)
	defer a.ForceSyncAPI()
	defer a.State.SetConfigSyncing(false)

	tunPayload := map[string]interface{}{"enable": enable}
	if dev := a.State.GetActualTunDevice(); dev != "" {
		tunPayload["device"] = dev
	}
	
	reqCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	
	if err := a.API.SyncConfigToKernel(reqCtx, map[string]interface{}{"tun": tunPayload}); err != nil {
		a.Cfg.Update(func(c *domain.TrayConfig) { c.Config.Tun.Enable = originalTun })
		if enable {
			a.State.SetTunRequestedTime(time.Time{})
		}
		
		if ctx.Err() == nil && a.ui != nil {
			if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
				a.ui.ShowError("TUN 设置失败", fmt.Sprintf("内核拒绝加载 TUN 配置，请检查驱动。\n\n详情: %v", err))
			} else {
				a.ui.ShowError("TUN 设置失败", "内核通信超时，操作已被丢弃。")
			}
		}
	}
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
	if a.State.GetPhase() != domain.PhaseRunning {
		if a.ui != nil {
			a.ui.ShowError("切换模式失败", "内核当前处于异常状态，操作已被丢弃。")
		}
		return
	}

	if mode == a.Cfg.GetConfig().Config.Mode {
		return
	}

	a.State.SetConfigSyncing(true)
	defer a.ForceSyncAPI()
	defer a.State.SetConfigSyncing(false)
	
	reqCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	
	if err := a.API.SyncConfigToKernel(reqCtx, map[string]interface{}{"mode": mode}); err != nil {
		if ctx.Err() == nil && a.ui != nil {
			a.ui.ShowError("切换模式失败", fmt.Sprintf("未能同步到内核，操作已丢弃。\n\n详情: %v", err))
		}
		return
	}
	
	a.Cfg.Update(func(c *domain.TrayConfig) { c.Config.Mode = mode })
}

func (a *Application) ToggleAllowLan(ctx context.Context, enable bool) {
	if a.State.GetPhase() != domain.PhaseRunning {
		if a.ui != nil {
			a.ui.ShowError("设置失败", "内核当前处于异常状态，操作已被丢弃。")
		}
		return
	}

	if enable == *a.Cfg.GetConfig().Config.AllowLan {
		return
	}

	a.State.SetConfigSyncing(true)
	defer a.ForceSyncAPI() 
	defer a.State.SetConfigSyncing(false)
	
	reqCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	
	if err := a.API.SyncConfigToKernel(reqCtx, map[string]interface{}{"allow-lan": enable}); err != nil {
		if ctx.Err() == nil && a.ui != nil {
			a.ui.ShowError("设置失败", fmt.Sprintf("未能同步到内核，操作已丢弃。\n\n详情: %v", err))
		}
		return
	}
	
	a.Cfg.Update(func(c *domain.TrayConfig) {
		b := enable
		c.Config.AllowLan = &b
	})
}
	
func (a *Application) ToggleSystemBrowser(enable bool) {
	a.Cfg.Update(func(c *domain.TrayConfig) {
		b := enable
		c.General.SystemBrowser = &b
	})
}

func (a *Application) ToggleRemoteWebUI(enable bool) {
	a.Cfg.Update(func(c *domain.TrayConfig) {
		b := enable
		c.General.RemoteWebUI = &b
	})
}

func (a *Application) GetPortConfigSnapshot() (mixed, socks, httpPort int) {
	cfg := a.Cfg.GetConfig()
	mixed = a.Cfg.GetEffectivePort(cfg.Config.MixedPort, domain.DefaultMixedPort)
	socks = a.Cfg.GetEffectivePort(cfg.Config.SocksPort, domain.DefaultSocksPort)
	httpPort = a.Cfg.GetEffectivePort(cfg.Config.Port, domain.DefaultPort)
	return
}

func (a *Application) GetControllerConfigSnapshot() (addr, secret string, online, sysBrowser bool, remoteURL string) {
	cfg := a.Cfg.GetConfig()
	addr = cfg.Config.ExternalController
	if addr == "" { addr = domain.DefaultExternalController }
	
	if cfg.Config.Secret != nil { secret = *cfg.Config.Secret }
	if cfg.General.RemoteWebUI != nil { online = *cfg.General.RemoteWebUI }
	if cfg.General.SystemBrowser != nil { sysBrowser = *cfg.General.SystemBrowser }
	if cfg.General.RemoteWebUIURL != nil { remoteURL = *cfg.General.RemoteWebUIURL }
	return
}

func (a *Application) ApplyPortConfig(ctx context.Context, mixed, socks, httpPort int) {
	cMixed, cSocks, cHttp := a.GetPortConfigSnapshot()
	if mixed == cMixed && socks == cSocks && httpPort == cHttp {
		return
	}

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

	if a.State.GetPhase() == domain.PhaseRunning {
		a.State.SetConfigSyncing(true)
		defer a.ForceSyncAPI() 
		defer a.State.SetConfigSyncing(false)

		reqCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		payload := map[string]interface{}{"mixed-port": mixed, "socks-port": socks, "port": httpPort}
		
		if err := a.API.SyncConfigToKernel(reqCtx, payload); err != nil {
			slog.Warn("热刷端口到内核失败，等待下次内核重载生效", "err", err)
			if ctx.Err() == nil && a.ui != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
				a.ui.ShowError("端口应用部分失败", fmt.Sprintf("配置已保存，但未能热刷入内核。将在下次配置重载时生效。\n\n详情: %v", err))
			}
		}
	}
}

func (a *Application) ApplyControllerConfig(addr, secret string, online, sysBrowser bool, remoteURL string) {
	cAddr, cSec, cOnline, cSys, cRemoteURL := a.GetControllerConfigSnapshot()
	coreChanged := (cAddr != addr) || (cSec != secret)
	appChanged := (cOnline != online) || (cSys != sysBrowser) || (cRemoteURL != remoteURL)

	if !coreChanged && !appChanged {
		return
	}

	a.Cfg.Update(func(c *domain.TrayConfig) {
		if coreChanged {
			c.Config.ExternalController = addr
			c.Config.Secret = &secret
		}
		bOnline, bSys, bRemoteURL := online, sysBrowser, remoteURL
		c.General.RemoteWebUI = &bOnline
		c.General.SystemBrowser = &bSys
		c.General.RemoteWebUIURL = &bRemoteURL
	})

	a.pushUIState()

	if coreChanged {
		slog.Info("Web 面板核心网络参数已变更，重启内核生效")
		if err := a.RestartKernel(context.Background()); err != nil && a.ui != nil {
			a.ui.ShowError("内核重启失败", err.Error())
		}
	}
}
	
func (a *Application) ForceSyncAPI() {
	select {
	case a.apiPollCh <- struct{}{}:
	default:
	}
}
