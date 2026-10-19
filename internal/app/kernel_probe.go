package app

import (
	"context"
	"fmt"
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

func checkPortDiff(cfgPort *int, kernelPort int) bool {
	if cfgPort == nil {
		return true
	}
	return *cfgPort != kernelPort
}

func (p *KernelProbe) PollStatus(ctx context.Context) ProbeResult {
	if p.st.IsExiting() || p.st.IsActionBusy() {
		return ProbeResult{}
	}

	qCtx, cancel := context.WithTimeout(ctx, kernelAPIPollTimeout)
	defer cancel()

	resp, err := p.api.GetKernelStatus(qCtx)
	if err != nil || resp == nil {
		return ProbeResult{}
	}

	if p.st.IsExiting() || p.st.IsActionBusy() {
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
	hasConfigChange := false

	modeChanged := resp.Mode != "" && resp.Mode != cfg.Config.Mode
	if modeChanged {
		slog.Info("内核路由模式更新", "from", cfg.Config.Mode, "to", resp.Mode)
		res.Changed = true
		hasConfigChange = true
	}

	logLevelChanged := resp.LogLevel != "" && resp.LogLevel != cfg.Config.LogLevel
	if logLevelChanged {
		slog.Info("内核日志等级更新", "from", cfg.Config.LogLevel, "to", resp.LogLevel)
		res.Changed = true
		hasConfigChange = true
	}

	unifiedDelayChanged := cfg.Config.UnifiedDelay == nil || *cfg.Config.UnifiedDelay != resp.UnifiedDelay
	if unifiedDelayChanged {
		fromStr := "未设置"
		if cfg.Config.UnifiedDelay != nil {
			fromStr = fmt.Sprintf("%v", *cfg.Config.UnifiedDelay)
		}
		slog.Info("内核统一延迟更新", "from", fromStr, "to", resp.UnifiedDelay)
		res.Changed = true
		hasConfigChange = true
	}

	allowLanChanged := resp.AllowLan != p.cfg.GetEffectiveAllowLan()
	if allowLanChanged {
		slog.Info("内核局域网共享更新", "to", resp.AllowLan)
		res.Changed = true
		hasConfigChange = true
	}

	mixedPortChanged := checkPortDiff(cfg.Config.MixedPort, resp.MixedPort)
	if mixedPortChanged {
		res.Changed = true
		res.PortChanged = true
		hasConfigChange = true
	}

	portChanged := checkPortDiff(cfg.Config.Port, resp.Port)
	if portChanged {
		res.Changed = true
		res.PortChanged = true
		hasConfigChange = true
	}

	socksPortChanged := checkPortDiff(cfg.Config.SocksPort, resp.SocksPort)
	if socksPortChanged {
		res.Changed = true
		hasConfigChange = true
	}

	tunChanged := p.reconcileTunStatus(cfg.Config.Tun.Enable, resp.Tun.Enable)
	if tunChanged {
		res.Changed = true
		hasConfigChange = true
	}

	if hasConfigChange {
		p.cfg.Update(func(c *domain.TrayConfig) {
			if modeChanged {
				c.Config.Mode = resp.Mode
			}
			if logLevelChanged {
				c.Config.LogLevel = resp.LogLevel
			}
			if unifiedDelayChanged {
				b := resp.UnifiedDelay
				c.Config.UnifiedDelay = &b
			}
			if allowLanChanged {
				b := resp.AllowLan
				c.Config.AllowLan = &b
			}
			if mixedPortChanged {
				val := resp.MixedPort
				c.Config.MixedPort = &val
			}
			if portChanged {
				val := resp.Port
				c.Config.Port = &val
			}
			if socksPortChanged {
				val := resp.SocksPort
				c.Config.SocksPort = &val
			}
			if tunChanged {
				c.Config.Tun.Enable = resp.Tun.Enable
			}
		})
	}

	effectiveTun := cfg.Config.Tun.Enable
	if tunChanged {
		effectiveTun = resp.Tun.Enable
	}
	if res.Changed && effectiveTun && !realAlive && !p.st.IsTunInGracePeriod() {
		slog.Warn("TUN 网卡未就绪", "device", currentActual)
	}

	return res
}

func (p *KernelProbe) reconcileTunStatus(wantTun, kernelTunEnabled bool) bool {
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
		} else {
			p.st.SetTunLostTime(time.Time{})
		}

		go func() {
			ticker := time.NewTicker(tunProbeBurstInterval)
			defer ticker.Stop()

			for i := 0; i < tunProbeBurstTimes; i++ {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if p.st.IsExiting() || p.st.IsActionBusy() {
						return
					}
					forceSync()
				}
			}
		}()
		pushUI()
	}
}
