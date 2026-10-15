package app

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/sys"
)

const (
	TunInitGracePeriod      = 20 * time.Second
	TunLostAlarmDelay       = 6 * time.Second
	tunProbeBurstInterval   = 300 * time.Millisecond
	tunProbeBurstTimes      = 3
	proxyRepairMaxAttempts  = 10
	proxyRepairPollInterval = 1000 * time.Millisecond
	kernelAPIPollTimeout    = 1500 * time.Millisecond
)

func (a *Application) getActualTunDevice() string {
	return a.State.GetActualTunDevice()
}

func (a *Application) isTunInGracePeriod() bool {
	reqTime := a.State.GetTunRequestedTime()
	lostTime := a.State.GetTunLostTime()

	reqActive := !reqTime.IsZero() && time.Since(reqTime) < TunInitGracePeriod
	lostActive := !lostTime.IsZero() && time.Since(lostTime) < TunLostAlarmDelay

	return reqActive || lostActive
}

func (a *Application) reconcileTunState(kernelTunEnabled bool) bool {
	wantTun := a.Cfg.GetConfig().Config.Tun.Enable

	if wantTun && kernelTunEnabled && a.State.IsTunAlive() && a.isTunInGracePeriod() {
		a.State.SetTunRequestedTime(time.Time{})
		slog.Debug("TUN 网卡就绪")
	}

	if kernelTunEnabled != wantTun {
		if wantTun && !kernelTunEnabled && a.isTunInGracePeriod() {
			if time.Since(a.State.GetTunRequestedTime()) < TunInitGracePeriod {
				slog.Debug("TUN 处于保护期，跳过同步")
				return false
			}
		}

		slog.Info("同步 TUN 状态", "expected", wantTun, "actual", kernelTunEnabled)
		a.Cfg.Update(func(c *domain.TrayConfig) {
			c.Config.Tun.Enable = kernelTunEnabled
		})
		return true
	}
	return false
}

func (a *Application) syncSystemProxy() {
	cfg := a.Cfg.GetConfig()
	wantEnable := cfg.General.SystemProxy != nil && *cfg.General.SystemProxy

	if !wantEnable {
		if err := sys.SetSystemProxy(false, ""); err != nil {
			slog.Error("关闭系统代理失败", "err", err)
		}
		return
	}

	portStr, hasPort := a.Cfg.GetEffectiveSystemProxyPortStr()
	if !hasPort {
		slog.Warn("Mixed 与 HTTP 端口均未开启，强制关闭系统代理")
		a.disableSystemProxySetting()

		if err := sys.SetSystemProxy(false, ""); err != nil {
			slog.Error("强制注销系统代理失败", "err", err)
		}

		if a.ui != nil {
			a.ui.ShowNotification("系统代理", "系统代理已关闭，请检查代理端口。")
		}
		return
	}

	slog.Debug("更新系统代理", "enabled", true, "port", portStr)
	if err := sys.SetSystemProxy(true, portStr); err != nil {
		slog.Error("设置系统代理失败", "err", err)
	}
}

func (a *Application) handleProxyStatusChange(ctx context.Context, status sys.ProxyStatus) {
	if a.State.IsExiting() {
		return
	}

	cfg := a.Cfg.GetConfig()
	if cfg.General.SystemProxy == nil || !*cfg.General.SystemProxy {
		return
	}

	expectedPort, ok := a.Cfg.GetEffectiveSystemProxyPortStr()
	if !ok {
		a.disableSystemProxySetting()
		return
	}

	expectedServer := domain.LocalhostIP + ":" + expectedPort

	if status.Enabled {
		if status.Server != "" && !strings.EqualFold(status.Server, expectedServer) {
			slog.Warn("系统代理被外部修改，停用本地代理", "server", status.Server, "expected", expectedServer)
			a.disableSystemProxySetting()
		}
		return
	}

	if !a.State.TryAcquireProxyRepair() {
		return
	}

	go a.repairProxyLoop(ctx, expectedPort, expectedServer)
}

func (a *Application) repairProxyLoop(ctx context.Context, port, expectedServer string) {
	defer a.State.ReleaseProxyRepair()

	for i := 1; i <= proxyRepairMaxAttempts; i++ {
		cfg := a.Cfg.GetConfig()
		if a.State.IsExiting() || ctx.Err() != nil || cfg.General.SystemProxy == nil || !*cfg.General.SystemProxy {
			return
		}

		a.syncSystemProxy()

		select {
		case <-ctx.Done():
			return
		case <-time.After(proxyRepairPollInterval):
		}

		cur, err := sys.GetProxyStatus()
		if err == nil && cur.Enabled && strings.EqualFold(cur.Server, expectedServer) {
			slog.Debug("系统代理已自动恢复", "attempt", i)
			return
		}
	}

	slog.Warn("系统代理持续被外部修改，已自动停用")
	a.disableSystemProxySetting()
}

func (a *Application) disableSystemProxySetting() {
	if a.State.IsExiting() {
		return
	}
	a.Cfg.Update(func(c *domain.TrayConfig) {
		b := false
		c.General.SystemProxy = &b
	})
	a.pushUIState()
}

func (a *Application) handleTunChange(ctx context.Context) {
	if a.State.IsExiting() || a.State.IsConfigSyncing() || a.State.IsRestarting() || a.State.IsReloading() {
		return
	}

	tunDev := a.getActualTunDevice()
	alive := sys.IsTunActive(tunDev)

	if a.State.IsTunAlive() != alive {
		slog.Info("TUN 网卡状态变更", "device", tunDev, "active", alive)
		a.State.SetTunAlive(alive)
		if !alive {
			a.State.SetTunLostTime(time.Now())
		}

		go func() {
			for i := 0; i < tunProbeBurstTimes; i++ {
				select {
				case <-ctx.Done():
					return
				case <-time.After(tunProbeBurstInterval):
				}
				a.ForceSyncAPI()
			}
		}()
		a.pushUIState()
	}
}

func (a *Application) syncAllConfig(ctx context.Context) {
	if a.State.GetPhase() != domain.PhaseRunning {
		return
	}
	cfg := a.Cfg.GetConfig()
	_ = a.API.SyncAllRuntime(
		ctx,
		cfg.Config.Mode,
		*cfg.Config.AllowLan,
		cfg.Config.Tun.Enable,
		a.State.GetActualTunDevice(),
	)
}

func (a *Application) pollKernelAPI(ctx context.Context) bool {
	if a.State.IsExiting() || a.State.IsReloading() || a.State.IsConfigSyncing() {
		return false
	}

	queryCtx, cancel := context.WithTimeout(ctx, kernelAPIPollTimeout)
	defer cancel()

	resp, err := a.API.GetKernelStatus(queryCtx)
	if err != nil {
		return false
	}

	changed := false
	currentActual := a.getActualTunDevice()

	if resp.Tun.Device != "" && resp.Tun.Device != currentActual {
		a.State.SetActualTunDevice(resp.Tun.Device)
		currentActual = resp.Tun.Device
		changed = true
	}

	realAlive := sys.IsTunActive(currentActual)
	if a.State.IsTunAlive() != realAlive {
		a.State.SetTunAlive(realAlive)
		changed = true
	}

	cfg := a.Cfg.GetConfig()
	isProxyEnabled := cfg.General.SystemProxy != nil && *cfg.General.SystemProxy

	if resp.Mode != "" && resp.Mode != cfg.Config.Mode {
		slog.Info("内核路由模式更新", "from", cfg.Config.Mode, "to", resp.Mode)
		a.Cfg.Update(func(c *domain.TrayConfig) {
			c.Config.Mode = resp.Mode
		})
		changed = true
	}

	expectedAllowLan := *cfg.Config.AllowLan
	if resp.AllowLan != expectedAllowLan {
		slog.Info("内核局域网共享更新", "from", expectedAllowLan, "to", resp.AllowLan)
		a.Cfg.Update(func(c *domain.TrayConfig) {
			b := resp.AllowLan
			c.Config.AllowLan = &b
		})
		changed = true
	}

	if resp.LogLevel != "" && resp.LogLevel != cfg.Config.LogLevel {
		slog.Debug("同步内核日志等级", "level", resp.LogLevel)
		a.Cfg.Update(func(c *domain.TrayConfig) {
			c.Config.LogLevel = resp.LogLevel
		})
		changed = true
	}

	if cfg.Config.UnifiedDelay != nil && resp.UnifiedDelay != *cfg.Config.UnifiedDelay {
		slog.Debug("同步内核统一延迟", "unified_delay", resp.UnifiedDelay)
		a.Cfg.Update(func(c *domain.TrayConfig) {
			b := resp.UnifiedDelay
			c.Config.UnifiedDelay = &b
		})
		changed = true
	}

	if cfg.Config.MixedPort == nil || *cfg.Config.MixedPort != resp.MixedPort {
		slog.Info("内核混合端口变更", "port", resp.MixedPort)
		a.Cfg.Update(func(c *domain.TrayConfig) {
			p := resp.MixedPort
			c.Config.MixedPort = &p
		})
		if isProxyEnabled {
			a.syncSystemProxy()
		}
		changed = true
	}

	if cfg.Config.Port == nil || *cfg.Config.Port != resp.Port {
		slog.Info("内核 HTTP 端口变更", "port", resp.Port)
		a.Cfg.Update(func(c *domain.TrayConfig) {
			p := resp.Port
			c.Config.Port = &p
		})
		if isProxyEnabled && (cfg.Config.MixedPort == nil || *cfg.Config.MixedPort <= 0) {
			a.syncSystemProxy()
		}
		changed = true
	}

	if cfg.Config.SocksPort == nil || *cfg.Config.SocksPort != resp.SocksPort {
		slog.Info("内核 Socks 端口变更", "port", resp.SocksPort)
		a.Cfg.Update(func(c *domain.TrayConfig) {
			p := resp.SocksPort
			c.Config.SocksPort = &p
		})
		changed = true
	}
	
	if a.reconcileTunState(resp.Tun.Enable) {
		changed = true
	}

	wantTun := a.Cfg.GetConfig().Config.Tun.Enable
	
	if changed && wantTun && !realAlive && !a.isTunInGracePeriod() {
		slog.Warn("TUN 网卡未就绪", "device", currentActual)
	}

	return changed
}
