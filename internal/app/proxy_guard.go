package app

import (
	"context"
	"log/slog"
	"time"

	"mihomo-tray/internal/config"
	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/state"
	"mihomo-tray/internal/sys"
)

const (
	proxyRepairMaxAttempts  = 10
	proxyRepairPollInterval = 1000 * time.Millisecond
)

type ProxyGuard struct {
	cfg     *config.Manager
	st      *state.RuntimeState
	onAlert func(title, msg string)
}

func NewProxyGuard(cfg *config.Manager, st *state.RuntimeState, onAlert func(title, msg string)) *ProxyGuard {
	return &ProxyGuard{
		cfg:     cfg,
		st:      st,
		onAlert: onAlert,
	}
}

func (g *ProxyGuard) SetProxy(enable bool) {
	g.cfg.Update(func(c *domain.TrayConfig) {
		b := enable
		c.General.SystemProxy = &b
	})
	g.Sync()
}

func (g *ProxyGuard) Sync() {
	wantEnable := g.cfg.IsSystemProxyEnabled()
	portStr, hasPort := g.cfg.GetEffectiveSystemProxyPortStr()

	if !wantEnable {
		if hasPort {
			if err := sys.DisableProxy(portStr); err != nil {
				slog.Error("关闭系统代理失败", "err", err)
			}
		}
		return
	}

	if !hasPort {
		slog.Warn("未检测到有效代理端口，强制关闭系统代理")
		g.DisableAndClearSetting()
		if g.onAlert != nil {
			g.onAlert("系统代理", "系统代理已关闭，请检查代理端口配置。")
		}
		return
	}

	slog.Debug("更新系统代理", "enabled", true, "port", portStr)
	if err := sys.EnableProxy(portStr); err != nil {
		slog.Error("设置系统代理失败", "err", err)
	}
}

func (g *ProxyGuard) HandleStatusChange(ctx context.Context, status sys.ProxyStatus) {
	if g.st.IsExiting() || !g.cfg.IsSystemProxyEnabled() {
		return
	}

	expectedPort, ok := g.cfg.GetEffectiveSystemProxyPortStr()
	if !ok {
		g.DisableAndClearSetting()
		return
	}

	expectedServer := domain.LocalhostIP + ":" + expectedPort

	if status.Enabled {
		if status.Server != "" && !status.Matches(expectedServer) {
			slog.Warn("检测到第三方代理劫持，停用本地接管", "server", status.Server, "expected", expectedServer)
			g.DisableAndClearSetting()
		}
		return
	}

	if !g.st.TryAcquireProxyRepair() {
		return
	}
	go g.repairLoop(ctx, expectedServer)
}

func (g *ProxyGuard) repairLoop(ctx context.Context, expectedServer string) {
	defer g.st.ReleaseProxyRepair()

	for i := 1; i <= proxyRepairMaxAttempts; i++ {
		if g.st.IsExiting() || ctx.Err() != nil || !g.cfg.IsSystemProxyEnabled() {
			return
		}
		g.Sync()

		select {
		case <-ctx.Done():
			return
		case <-time.After(proxyRepairPollInterval):
		}

		cur, err := sys.GetProxyStatus()
		if err == nil && cur.Enabled && cur.Matches(expectedServer) {
			slog.Debug("系统代理自愈成功", "attempt", i)
			return
		}
	}
	slog.Warn("系统代理持续被外部修改或自愈超时，自动解除接管")
	g.DisableAndClearSetting()
}

func (g *ProxyGuard) DisableAndClearSetting() {
	if g.st.IsExiting() {
		return
	}
	g.cfg.Update(func(c *domain.TrayConfig) {
		b := false
		c.General.SystemProxy = &b
	})

	if portStr, hasPort := g.cfg.GetEffectiveSystemProxyPortStr(); hasPort {
		if err := sys.DisableProxy(portStr); err != nil {
			slog.Error("注销系统代理注册表失败", "err", err)
		}
	}
}

func (g *ProxyGuard) SafeShutdownCleanup() {
	portStr, hasPort := g.cfg.GetEffectiveSystemProxyPortStr()
	if !hasPort {
		return
	}

	if err := sys.DisableProxy(portStr); err != nil {
		slog.Error("退出时注销系统代理失败", "err", err)
	} else {
		slog.Debug("退出时系统代理安全清理完成")
	}
}
