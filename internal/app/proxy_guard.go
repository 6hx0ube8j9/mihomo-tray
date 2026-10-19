package app

import (
	"context"
	"log/slog"
	"sync"
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

	mu              sync.Mutex
	lastAppliedPort string
}

func NewProxyGuard(cfg *config.Manager, st *state.RuntimeState, onAlert func(title, msg string)) *ProxyGuard {
	return &ProxyGuard{
		cfg:     cfg,
		st:      st,
		onAlert: onAlert,
	}
}

func (g *ProxyGuard) SetProxy(enable bool) {
	if g.st.IsExiting() {
		return
	}

	if !enable {
		g.disableInternal()
		return
	}

	g.cfg.Update(func(c *domain.TrayConfig) {
		b := true
		c.General.SystemProxy = &b
	})
	g.Sync()
}

func (g *ProxyGuard) Sync() {
	if g.st.IsExiting() {
		return
	}

	wantEnable := g.cfg.IsSystemProxyEnabled()
	portStr, hasPort := g.cfg.GetEffectiveSystemProxyPortStr()

	if !wantEnable {
		g.cleanupRegistryProxy(portStr)
		return
	}

	if !hasPort {
		slog.Warn("未检测到有效代理端口，已停用系统代理")
		g.disableInternal()
		if g.onAlert != nil {
			g.onAlert("系统代理", "未检测到有效代理端口，系统代理已关闭")
		}
		return
	}

	slog.Debug("设置系统代理", "enabled", true, "port", portStr)
	if err := sys.EnableProxy(portStr); err != nil {
		slog.Error("设置系统代理失败", "err", err)
		return
	}

	g.mu.Lock()
	g.lastAppliedPort = portStr
	g.mu.Unlock()
}

func (g *ProxyGuard) cleanupRegistryProxy(currentPortStr string) {
	g.mu.Lock()
	targetPort := g.lastAppliedPort
	if targetPort == "" {
		targetPort = currentPortStr
	}
	g.mu.Unlock()

	if targetPort == "" {
		return
	}

	if err := sys.DisableProxy(targetPort); err != nil {
		slog.Error("注销系统代理失败", "port", targetPort, "err", err)
	} else {
		g.mu.Lock()
		g.lastAppliedPort = ""
		g.mu.Unlock()
	}
}

func (g *ProxyGuard) disableInternal() {
	if g.st.IsExiting() {
		return
	}

	if g.cfg.IsSystemProxyEnabled() {
		g.cfg.Update(func(c *domain.TrayConfig) {
			b := false
			c.General.SystemProxy = &b
		})
	}

	portStr, _ := g.cfg.GetEffectiveSystemProxyPortStr()
	g.cleanupRegistryProxy(portStr)
}

func (g *ProxyGuard) SafeShutdownCleanup() {
	portStr, _ := g.cfg.GetEffectiveSystemProxyPortStr()
	g.cleanupRegistryProxy(portStr)
	slog.Debug("退出清理完成")
}

func (g *ProxyGuard) HandleStatusChange(ctx context.Context, status sys.ProxyStatus) {
	if g.st.IsExiting() || !g.cfg.IsSystemProxyEnabled() {
		return
	}

	expectedPort, ok := g.cfg.GetEffectiveSystemProxyPortStr()
	if !ok {
		g.disableInternal()
		return
	}

	expectedServer := domain.LocalhostIP + ":" + expectedPort

	if status.Enabled {
		if status.Server != "" && !status.Matches(expectedServer) {
			slog.Warn("检测到外部代理配置变更，停止接管", "server", status.Server, "expected", expectedServer)
			g.disableInternal()
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
			slog.Debug("系统代理同步完成", "attempt", i)
			return
		}
	}
	slog.Warn("系统代理持续被外部修改或同步超时，已解除接管")
	g.disableInternal()
}
