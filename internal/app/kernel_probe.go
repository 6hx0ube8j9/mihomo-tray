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

const kernelAPIPollTimeout = 1500 * time.Millisecond

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

type KernelDiff struct {
	Mode         *string
	LogLevel     *string
	UnifiedDelay *bool
	AllowLan     *bool
	MixedPort    *int
	Port         *int
	SocksPort    *int
	TunEnable    *bool
}

func (d *KernelDiff) HasChanges() bool {
	return d.Mode != nil || d.LogLevel != nil || d.UnifiedDelay != nil ||
		d.AllowLan != nil || d.MixedPort != nil || d.Port != nil ||
		d.SocksPort != nil || d.TunEnable != nil
}

func (d *KernelDiff) Apply(c *domain.TrayConfig) {
	if d.Mode != nil {
		c.Config.Mode = *d.Mode
	}
	if d.LogLevel != nil {
		c.Config.LogLevel = *d.LogLevel
	}
	if d.UnifiedDelay != nil {
		c.Config.UnifiedDelay = d.UnifiedDelay
	}
	if d.AllowLan != nil {
		c.Config.AllowLan = d.AllowLan
	}
	if d.MixedPort != nil {
		c.Config.MixedPort = d.MixedPort
	}
	if d.Port != nil {
		c.Config.Port = d.Port
	}
	if d.SocksPort != nil {
		c.Config.SocksPort = d.SocksPort
	}
	if d.TunEnable != nil {
		c.Config.Tun.Enable = *d.TunEnable
	}
}

type ProbeResult struct {
	Changed        bool
	PortChanged    bool
	TunRollback    bool
	RollbackReason string
	Diff           KernelDiff
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

	tunRollback, reason, tunEnableDiff, tunChanged := p.reconcileTunStatus(wantTun, resp.Tun.Enable, resp.Tun.Device, targetDev, realAlive)
	if tunRollback {
		res.TunRollback = true
		res.RollbackReason = reason
		res.Changed = true
	}
	if tunChanged {
		res.Changed = true
	}
	if tunEnableDiff != nil {
		res.Diff.TunEnable = tunEnableDiff
	}

	if resp.Mode != "" && resp.Mode != cfg.Config.Mode {
		slog.Info("内核路由模式更新", "from", cfg.Config.Mode, "to", resp.Mode)
		m := resp.Mode
		res.Diff.Mode = &m
		res.Changed = true
	}

	if resp.LogLevel != "" && resp.LogLevel != cfg.Config.LogLevel {
		slog.Info("内核日志等级更新", "from", cfg.Config.LogLevel, "to", resp.LogLevel)
		l := resp.LogLevel
		res.Diff.LogLevel = &l
		res.Changed = true
	}

	if cfg.Config.UnifiedDelay == nil || *cfg.Config.UnifiedDelay != resp.UnifiedDelay {
		u := resp.UnifiedDelay
		res.Diff.UnifiedDelay = &u
		res.Changed = true
	}

	if resp.AllowLan != p.cfg.GetEffectiveAllowLan() {
		slog.Info("内核局域网共享更新", "to", resp.AllowLan)
		a := resp.AllowLan
		res.Diff.AllowLan = &a
		res.Changed = true
	}

	if checkPortDiff(cfg.Config.MixedPort, resp.MixedPort) {
		mp := resp.MixedPort
		res.Diff.MixedPort = &mp
		res.Changed = true
		res.PortChanged = true
	}

	if checkPortDiff(cfg.Config.Port, resp.Port) {
		pt := resp.Port
		res.Diff.Port = &pt
		res.Changed = true
		res.PortChanged = true
	}

	if checkPortDiff(cfg.Config.SocksPort, resp.SocksPort) {
		sp := resp.SocksPort
		res.Diff.SocksPort = &sp
		res.Changed = true
	}

	return res
}

func (p *KernelProbe) reconcileTunStatus(
	wantTun, kernelEnable bool,
	kernelDevice, targetDev string,
	realAlive bool,
) (rollback bool, reason string, tunDiff *bool, changed bool) {
	bTrue, bFalse := true, false

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
				return true, fmt.Sprintf("虚拟网卡 [%s] 已被其他程序占用", targetDev), nil, true
			}
			return false, "", nil, false
		}

		if !kernelEnable && !realAlive {
			slog.Info("检测到外部已关闭 TUN 模式，同步本地配置", "device", targetDev)
			p.st.ClearTunLost()
			return false, "", &bFalse, true
		}

		if kernelEnable && !realAlive {
			if !p.st.IsTunDropDebouncing() {
				slog.Warn("TUN 虚拟网卡掉线已超过 6 秒", "device", targetDev)
			}
		}

		return false, "", nil, false
	}

	if !wantTun && kernelEnable && realAlive {
		slog.Info("检测到外部面板已开启 TUN 模式，同步本地配置", "device", targetDev)
		p.st.ClearTunStarting()
		p.st.ClearTunLost()
		return false, "", &bTrue, true
	}

	return false, "", nil, false
}
