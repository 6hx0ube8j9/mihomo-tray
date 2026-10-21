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
	kernelAPIPollTimeout = 1500 * time.Millisecond
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
	Changed        bool
	PortChanged    bool
	TunRollback    bool
	RollbackReason string
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

	targetDev := resp.Tun.Device
	if targetDev == "" {
		targetDev = p.st.GetActualTunDevice()
		if targetDev == "" {
			targetDev = domain.DefaultTunDevice
		}
	} else if targetDev != p.st.GetActualTunDevice() {
		p.st.SetActualTunDevice(targetDev)
		res.Changed = true
	}

	realAlive := sys.IsTunActive(targetDev)
	if p.st.IsTunAlive() != realAlive {
		p.st.SetTunAlive(realAlive)
		if !realAlive {
			p.st.SetTunLostTime(time.Now())
		} else {
			p.st.ClearTunLost()
		}
		res.Changed = true
	}

	cfg := p.cfg.GetConfig()
	wantTun := cfg.Config.Tun.Enable

	tunRollback, reason, tunChanged := p.reconcileTunStatus(wantTun, resp.Tun.Enable, resp.Tun.Device, targetDev, realAlive)
	if tunRollback {
		res.TunRollback = true
		res.RollbackReason = reason
		res.Changed = true
	}
	if tunChanged {
		res.Changed = true
	}

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

	mixedPortChanged := resp.MixedPort > 0 && checkPortDiff(cfg.Config.MixedPort, resp.MixedPort)
	if mixedPortChanged {
		res.Changed = true
		res.PortChanged = true
		hasConfigChange = true
	}

	portChanged := resp.Port > 0 && checkPortDiff(cfg.Config.Port, resp.Port)
	if portChanged {
		res.Changed = true
		res.PortChanged = true
		hasConfigChange = true
	}

	socksPortChanged := resp.SocksPort > 0 && checkPortDiff(cfg.Config.SocksPort, resp.SocksPort)
	if socksPortChanged {
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
		})
	}

	return res
}

func (p *KernelProbe) reconcileTunStatus(wantTun, kernelEnable bool, kernelDevice, targetDev string, realAlive bool) (rollback bool, reason string, changed bool) {
	if wantTun {
		if kernelEnable && realAlive {
			if p.st.IsTunStarting() {
				p.st.ClearTunStarting()
				slog.Info("TUN 模式已成功就绪", "device", targetDev)
				return false, "", true
			}
			return false, "", false
		}

		if p.st.IsTunStartingTimedOut() {
			slog.Warn("TUN 模式启动流程超时", "device", targetDev)
			return true, "TUN 模式启动超时 (20s)", true
		}

		if p.st.IsTunStarting() {
			if !kernelEnable && kernelDevice != "" && realAlive {
				slog.Warn("检测到虚拟网卡已被占用冲突", "device", targetDev)
				return true, fmt.Sprintf("虚拟网卡 [%s] 已被其他程序占用或冲突", targetDev), true
			}
			return false, "", false
		}

		if !kernelEnable && !realAlive {
			slog.Info("检测到外部已关闭 TUN 模式，同步至本地状态", "device", targetDev)
			p.cfg.Update(func(c *domain.TrayConfig) {
				c.Config.Tun.Enable = false
			})
			p.st.ClearTunLost()
			return false, "", true
		}

		if kernelEnable && !realAlive {
			if !p.st.IsTunDropDebouncing() {
				slog.Warn("TUN 虚拟网卡掉线已超过 6 秒", "device", targetDev)
			}
		}

		return false, "", false
	}

	if !wantTun && kernelEnable && realAlive {
		slog.Info("检测到外部面板已开启 TUN 模式，同步至本地状态", "device", targetDev)
		p.cfg.Update(func(c *domain.TrayConfig) {
			c.Config.Tun.Enable = true
		})
		p.st.ClearTunStarting()
		p.st.ClearTunLost()
		return false, "", true
	}

	return false, "", false
}

func (p *KernelProbe) HandleTunChange(forceSync func()) {
	if p.st.IsExiting() || p.st.IsActionBusy() {
		return
	}
	
	if forceSync != nil {
		forceSync()
	}
}
