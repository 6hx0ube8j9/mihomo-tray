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

	ApplyPatch func(c *domain.TrayConfig)
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
	var patches []func(c *domain.TrayConfig)

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

	tunRollback, reason, tunPatch, tunChanged := p.reconcileTunStatus(wantTun, resp.Tun.Enable, resp.Tun.Device, targetDev, realAlive)
	if tunRollback {
		res.TunRollback = true
		res.RollbackReason = reason
		res.Changed = true
	}
	if tunChanged {
		res.Changed = true
	}
	if tunPatch != nil {
		patches = append(patches, tunPatch)
	}

	modeChanged := resp.Mode != "" && resp.Mode != cfg.Config.Mode
	if modeChanged {
		slog.Info("内核路由模式更新", "from", cfg.Config.Mode, "to", resp.Mode)
		res.Changed = true
	}

	logLevelChanged := resp.LogLevel != "" && resp.LogLevel != cfg.Config.LogLevel
	if logLevelChanged {
		slog.Info("内核日志等级更新", "from", cfg.Config.LogLevel, "to", resp.LogLevel)
		res.Changed = true
	}

	unifiedDelayChanged := cfg.Config.UnifiedDelay == nil || *cfg.Config.UnifiedDelay != resp.UnifiedDelay
	if unifiedDelayChanged {
		fromStr := "未设置"
		if cfg.Config.UnifiedDelay != nil {
			fromStr = fmt.Sprintf("%v", *cfg.Config.UnifiedDelay)
		}
		slog.Info("内核统一延迟更新", "from", fromStr, "to", resp.UnifiedDelay)
		res.Changed = true
	}

	allowLanChanged := resp.AllowLan != p.cfg.GetEffectiveAllowLan()
	if allowLanChanged {
		slog.Info("内核局域网共享更新", "to", resp.AllowLan)
		res.Changed = true
	}

	mixedPortChanged := resp.MixedPort > 0 && checkPortDiff(cfg.Config.MixedPort, resp.MixedPort)
	if mixedPortChanged {
		res.Changed = true
		res.PortChanged = true
	}

	portChanged := resp.Port > 0 && checkPortDiff(cfg.Config.Port, resp.Port)
	if portChanged {
		res.Changed = true
		res.PortChanged = true
	}

	socksPortChanged := resp.SocksPort > 0 && checkPortDiff(cfg.Config.SocksPort, resp.SocksPort)
	if socksPortChanged {
		res.Changed = true
	}

	hasConfigDiff := modeChanged || logLevelChanged || unifiedDelayChanged ||
		allowLanChanged || mixedPortChanged || portChanged || socksPortChanged

	if hasConfigDiff {
		mode := resp.Mode
		logLevel := resp.LogLevel
		unifiedDelay := resp.UnifiedDelay
		allowLan := resp.AllowLan
		mixedPort := resp.MixedPort
		port := resp.Port
		socksPort := resp.SocksPort

		patches = append(patches, func(c *domain.TrayConfig) {
			if modeChanged {
				c.Config.Mode = mode
			}
			if logLevelChanged {
				c.Config.LogLevel = logLevel
			}
			if unifiedDelayChanged {
				b := unifiedDelay
				c.Config.UnifiedDelay = &b
			}
			if allowLanChanged {
				b := allowLan
				c.Config.AllowLan = &b
			}
			if mixedPortChanged {
				val := mixedPort
				c.Config.MixedPort = &val
			}
			if portChanged {
				val := port
				c.Config.Port = &val
			}
			if socksPortChanged {
				val := socksPort
				c.Config.SocksPort = &val
			}
		})
	}

	if len(patches) > 0 {
		res.ApplyPatch = func(c *domain.TrayConfig) {
			for _, patch := range patches {
				patch(c)
			}
		}
	}

	return res
}

func (p *KernelProbe) reconcileTunStatus(
	wantTun, kernelEnable bool,
	kernelDevice, targetDev string,
	realAlive bool,
) (rollback bool, reason string, patch func(c *domain.TrayConfig), changed bool) {
	if wantTun {
		if kernelEnable && realAlive {
			if p.st.IsTunStarting() {
				p.st.ClearTunStarting()
				slog.Info("TUN 模式已成功就绪", "device", targetDev)
				return false, "", nil, true
			}
			return false, "", nil, false
		}

		if p.st.IsTunStartingTimedOut() {
			slog.Warn("TUN 模式启动流程超时", "device", targetDev)
			return true, "TUN 模式启动超时 (20s)", nil, true
		}

		if p.st.IsTunStarting() {
			if !kernelEnable && kernelDevice != "" && realAlive {
				slog.Warn("检测到虚拟网卡已被占用冲突", "device", targetDev)
				return true, fmt.Sprintf("虚拟网卡 [%s] 已被其他程序占用或冲突", targetDev), nil, true
			}
			return false, "", nil, false
		}

		if !kernelEnable && !realAlive {
			slog.Info("检测到外部已关闭 TUN 模式，生成同步闭包", "device", targetDev)
			p.st.ClearTunLost()
			return false, "", func(c *domain.TrayConfig) {
				c.Config.Tun.Enable = false
			}, true
		}

		if kernelEnable && !realAlive {
			if !p.st.IsTunDropDebouncing() {
				slog.Warn("TUN 虚拟网卡掉线已超过 6 秒", "device", targetDev)
			}
		}

		return false, "", nil, false
	}

	if !wantTun && kernelEnable && realAlive {
		slog.Info("检测到外部面板已开启 TUN 模式，生成同步闭包", "device", targetDev)
		p.st.ClearTunStarting()
		p.st.ClearTunLost()
		return false, "", func(c *domain.TrayConfig) {
			c.Config.Tun.Enable = true
		}, true
	}

	return false, "", nil, false
}

func (p *KernelProbe) HandleTunChange(forceSync func()) {
	if p.st.IsExiting() || p.st.IsActionBusy() {
		return
	}

	if forceSync != nil {
		forceSync()
	}
}
