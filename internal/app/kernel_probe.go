package app

import (
	"context"
	"log/slog"
	"time"

	"mihomo-tray/internal/config"
	"mihomo-tray/internal/core"
	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/state"
	"mihomo-tray/internal/sys"
)

const (
	tunProbeBurstInterval = 300 * time.Millisecond
	tunProbeBurstTimes    = 3
	kernelAPIPollTimeout  = 1500 * time.Millisecond
)

type KernelProbe struct {
	cfg *config.Manager
	st  *state.RuntimeState
	api *core.APIClient
}

func NewKernelProbe(cfg *config.Manager, st *state.RuntimeState, api *core.APIClient) *KernelProbe {
	return &KernelProbe{
		cfg: cfg,
		st:  st,
		api: api,
	}
}

type ProbeResult struct {
	Changed     bool
	PortChanged bool
}

func (p *KernelProbe) PollStatus(ctx context.Context) ProbeResult {
	if p.st.IsExiting() || p.st.IsActionBusy() {
		return ProbeResult{}
	}

	qCtx, cancel := context.WithTimeout(ctx, kernelAPIPollTimeout)
	defer cancel()

	resp, err := p.api.GetKernelStatus(qCtx)
	if err != nil {
		return ProbeResult{}
	}

	res := ProbeResult{}
	currentActual := p.st.GetActualTunDevice()

	if resp.Tun.Device != "" && resp.Tun.Device != currentActual {
		p.st.SetActualTunDevice(resp.Tun.Device)
		currentActual = resp.Tun.Device
		res.Changed = true
	}

	realAlive := sys.IsTunActive(currentActual)
	if p.st.IsTunAlive() != realAlive {
		p.st.SetTunAlive(realAlive)
		res.Changed = true
	}

	cfg := p.cfg.GetConfig()

	if resp.Mode != "" && resp.Mode != cfg.Config.Mode {
		slog.Info("内核路由模式更新", "from", cfg.Config.Mode, "to", resp.Mode)
		p.cfg.Update(func(c *domain.TrayConfig) { c.Config.Mode = resp.Mode })
		res.Changed = true
	}

	if resp.LogLevel != "" && resp.LogLevel != cfg.Config.LogLevel {
		slog.Info("内核日志等级更新", "from", cfg.Config.LogLevel, "to", resp.LogLevel)
		p.cfg.Update(func(c *domain.TrayConfig) { c.Config.LogLevel = resp.LogLevel })
		res.Changed = true
	}

	if cfg.Config.UnifiedDelay != nil && resp.UnifiedDelay != *cfg.Config.UnifiedDelay {
		slog.Info("内核统一延迟更新", "from", *cfg.Config.UnifiedDelay, "to", resp.UnifiedDelay)
		p.cfg.Update(func(c *domain.TrayConfig) {
			b := resp.UnifiedDelay
			c.Config.UnifiedDelay = &b
		})
		res.Changed = true
	}

	if resp.AllowLan != p.cfg.GetEffectiveAllowLan() {
		slog.Info("内核局域网共享更新", "to", resp.AllowLan)
		p.cfg.Update(func(c *domain.TrayConfig) {
			b := resp.AllowLan
			c.Config.AllowLan = &b
		})
		res.Changed = true
	}

	if cfg.Config.MixedPort == nil || *cfg.Config.MixedPort != resp.MixedPort {
		p.cfg.Update(func(c *domain.TrayConfig) {
			val := resp.MixedPort
			c.Config.MixedPort = &val
		})
		res.Changed = true
		res.PortChanged = true
	}

	if cfg.Config.Port == nil || *cfg.Config.Port != resp.Port {
		p.cfg.Update(func(c *domain.TrayConfig) {
			val := resp.Port
			c.Config.Port = &val
		})
		res.Changed = true
		res.PortChanged = true
	}

	if cfg.Config.SocksPort == nil || *cfg.Config.SocksPort != resp.SocksPort {
		p.cfg.Update(func(c *domain.TrayConfig) {
			val := resp.SocksPort
			c.Config.SocksPort = &val
		})
		res.Changed = true
	}

	if p.reconcileTunState(resp.Tun.Enable) {
		res.Changed = true
	}

	if res.Changed && p.cfg.GetConfig().Config.Tun.Enable && !realAlive && !p.st.IsTunInGracePeriod() {
		slog.Warn("TUN 网卡未就绪", "device", currentActual)
	}

	return res
}

func (p *KernelProbe) reconcileTunState(kernelTunEnabled bool) bool {
	wantTun := p.cfg.GetConfig().Config.Tun.Enable
	if wantTun && kernelTunEnabled && p.st.IsTunAlive() && p.st.IsTunInGracePeriod() {
		p.st.SetTunRequestedTime(time.Time{})
		slog.Debug("TUN 网卡已进入就绪状态")
	}

	if kernelTunEnabled != wantTun {
		if wantTun && !kernelTunEnabled && p.st.IsTunInGracePeriod() {
			if time.Since(p.st.GetTunRequestedTime()) < state.TunInitGracePeriod {
				return false
			}
		}
		slog.Info("同步 TUN 状态变更", "expected", wantTun, "actual", kernelTunEnabled)
		p.cfg.Update(func(c *domain.TrayConfig) { c.Config.Tun.Enable = kernelTunEnabled })
		return true
	}
	return false
}

func (p *KernelProbe) HandleTunChange(ctx context.Context, forceSync func(), pushUI func()) {
	if p.st.IsExiting() || p.st.IsActionBusy() {
		return
	}
	tunDev := p.st.GetActualTunDevice()
	alive := sys.IsTunActive(tunDev)
	if p.st.IsTunAlive() != alive {
		slog.Info("TUN 网卡状态变更", "device", tunDev, "active", alive)
		p.st.SetTunAlive(alive)
		if !alive {
			p.st.SetTunLostTime(time.Now())
		}
		go func() {
			for i := 0; i < tunProbeBurstTimes; i++ {
				select {
				case <-ctx.Done():
					return
				case <-time.After(tunProbeBurstInterval):
				}
				forceSync()
			}
		}()
		pushUI()
	}
}
