package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"mihomo-tray/internal/domain"
)

func (a *Application) ToggleTun(ctx context.Context, enable bool) (restarted bool, err error) {
	originalTun := a.Cfg.GetConfig().Config.Tun.Enable
	if originalTun == enable {
		return false, nil
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
		return true, nil
	}

	if a.State.GetPhase() != domain.PhaseRunning {
		a.Cfg.Update(func(c *domain.TrayConfig) { c.Config.Tun.Enable = originalTun })
		if enable {
			a.State.SetTunRequestedTime(time.Time{})
		}
		return false, fmt.Errorf("内核尚未就绪，无法应用 TUN 设置")
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

	if syncErr := a.API.SyncConfigToKernel(reqCtx, map[string]interface{}{"tun": tunPayload}); syncErr != nil {
		a.Cfg.Update(func(c *domain.TrayConfig) { c.Config.Tun.Enable = originalTun })
		if enable {
			a.State.SetTunRequestedTime(time.Time{})
		}

		if ctx.Err() == nil {
			if errors.Is(syncErr, context.DeadlineExceeded) || errors.Is(syncErr, context.Canceled) {
				return false, fmt.Errorf("与内核通信超时，操作已取消")
			}
			return false, fmt.Errorf("内核拒绝加载 TUN 设置，请检查虚拟网卡驱动: %w", syncErr)
		}
	}
	return false, nil
}

func (a *Application) ToggleProxy(enable bool) {
	a.Cfg.Update(func(c *domain.TrayConfig) {
		b := enable
		c.General.SystemProxy = &b
	})
	a.syncSystemProxy()
}

func (a *Application) SwitchMode(ctx context.Context, mode string) error {
	if a.State.GetPhase() != domain.PhaseRunning {
		return fmt.Errorf("内核尚未就绪，无法切换路由模式")
	}

	if mode == a.Cfg.GetConfig().Config.Mode {
		return nil
	}

	a.State.SetConfigSyncing(true)
	defer a.ForceSyncAPI()
	defer a.State.SetConfigSyncing(false)

	reqCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	if err := a.API.SyncConfigToKernel(reqCtx, map[string]interface{}{"mode": mode}); err != nil {
		return fmt.Errorf("同步路由模式至内核失败: %w", err)
	}

	a.Cfg.Update(func(c *domain.TrayConfig) { c.Config.Mode = mode })
	return nil
}

func (a *Application) ToggleAllowLan(ctx context.Context, enable bool) error {
	if a.State.GetPhase() != domain.PhaseRunning {
		return fmt.Errorf("内核尚未就绪，无法更改局域网设置")
	}

	if enable == *a.Cfg.GetConfig().Config.AllowLan {
		return nil
	}

	a.State.SetConfigSyncing(true)
	defer a.ForceSyncAPI()
	defer a.State.SetConfigSyncing(false)

	reqCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	if err := a.API.SyncConfigToKernel(reqCtx, map[string]interface{}{"allow-lan": enable}); err != nil {
		return fmt.Errorf("同步局域网设置至内核失败: %w", err)
	}

	a.Cfg.Update(func(c *domain.TrayConfig) {
		b := enable
		c.Config.AllowLan = &b
	})
	return nil
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
	mixed = a.Cfg.GetEffectiveMixedPort()
	socks = a.Cfg.GetEffectivePort(cfg.Config.SocksPort, domain.DefaultSocksPort)
	httpPort = a.Cfg.GetEffectivePort(cfg.Config.Port, domain.DefaultPort)
	return
}

func (a *Application) GetControllerConfigSnapshot() (addr, secret string, online, sysBrowser bool, remoteURL string) {
	cfg := a.Cfg.GetConfig()
	addr = cfg.Config.ExternalController
	if addr == "" {
		addr = domain.DefaultExternalController
	}
	secret = a.Cfg.GetEffectiveSecret(cfg.Config.Secret)
	if cfg.General.RemoteWebUI != nil {
		online = *cfg.General.RemoteWebUI
	}
	if cfg.General.SystemBrowser != nil {
		sysBrowser = *cfg.General.SystemBrowser
	}
	if cfg.General.RemoteWebUIURL != nil {
		remoteURL = *cfg.General.RemoteWebUIURL
	}
	return
}

func (a *Application) ApplyPortConfig(ctx context.Context, mixed, socks, httpPort int) error {
	cMixed, cSocks, cHttp := a.GetPortConfigSnapshot()
	if mixed == cMixed && socks == cSocks && httpPort == cHttp {
		return nil
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
			if ctx.Err() == nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
				return fmt.Errorf("端口配置已保存，但动态同步至内核失败，将在下次重启时生效: %w", err)
			}
		}
	}
	return nil
}

func (a *Application) ApplyControllerConfig(addr, secret string, online, sysBrowser bool, remoteURL string) error {
	cAddr, cSec, cOnline, cSys, cRemoteURL := a.GetControllerConfigSnapshot()
	coreChanged := (cAddr != addr) || (cSec != secret)
	appChanged := (cOnline != online) || (cSys != sysBrowser) || (cRemoteURL != remoteURL)

	if !coreChanged && !appChanged {
		return nil
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
		slog.Info("Web 面板鉴权参数已变更，触发内核物理重启以绑定新地址与密码")
		if err := a.RestartKernel(context.Background()); err != nil {
			return fmt.Errorf("参数已保存，但内核重启失败: %w", err)
		}
	}
	return nil
}

func (a *Application) ForceSyncAPI() {
	select {
	case a.apiPollCh <- struct{}{}:
	default:
	}
}
