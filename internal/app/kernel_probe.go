package app

import (
	"context"
	"log/slog"
	"time"

	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/sys"
)

const (
	TunInitGracePeriod    = 20 * time.Second
	TunLostAlarmDelay     = 6 * time.Second
	tunProbeBurstInterval = 300 * time.Millisecond
	tunProbeBurstTimes    = 3
	kernelAPIPollTimeout  = 1500 * time.Millisecond
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
		slog.Debug("TUN 网卡已进入就绪状态")
	}

	if kernelTunEnabled != wantTun {
		if wantTun && !kernelTunEnabled && a.isTunInGracePeriod() {
			if time.Since(a.State.GetTunRequestedTime()) < TunInitGracePeriod {
				slog.Debug("TUN 处于保护期，跳过外部反向覆盖")
				return false
			}
		}

		slog.Info("同步 TUN 状态变更", "expected", wantTun, "actual", kernelTunEnabled)
		a.Cfg.Update(func(c *domain.TrayConfig) {
			c.Config.Tun.Enable = kernelTunEnabled
		})
		return true
	}
	return false
}

func (a *Application) handleTunChange(ctx context.Context) {
	if a.State.IsExiting() || a.State.IsActionBusy() {
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
		a.Cfg.GetEffectiveAllowLan(),
		cfg.Config.Tun.Enable,
		a.State.GetActualTunDevice(),
	)
}

func (a *Application) pollKernelAPI(ctx context.Context) bool {
	if a.State.IsExiting() || a.State.IsActionBusy() {
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
	isProxyEnabled := a.Cfg.IsSystemProxyEnabled()

	if resp.Mode != "" && resp.Mode != cfg.Config.Mode {
		slog.Info("内核路由模式更新", "from", cfg.Config.Mode, "to", resp.Mode)
		a.Cfg.Update(func(c *domain.TrayConfig) {
			c.Config.Mode = resp.Mode
		})
		changed = true
	}

	expectedAllowLan := a.Cfg.GetEffectiveAllowLan()
	if resp.AllowLan != expectedAllowLan {
		slog.Info("内核局域网共享更新", "from", expectedAllowLan, "to", resp.AllowLan)
		a.Cfg.Update(func(c *domain.TrayConfig) {
			b := resp.AllowLan
			c.Config.AllowLan = &b
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

	if changed && a.Cfg.GetConfig().Config.Tun.Enable && !realAlive && !a.isTunInGracePeriod() {
		slog.Warn("TUN 网卡未就绪", "device", currentActual)
	}

	return changed
}
