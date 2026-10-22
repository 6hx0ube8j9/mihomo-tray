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
	proxyQuickReconcileMaxAttempts = 3
	proxyQuickReconcileInterval    = 500 * time.Millisecond
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

	g.mu.Lock()
	defer g.mu.Unlock()

	wantEnable := g.cfg.IsSystemProxyEnabled()
	portStr, hasPort := g.cfg.GetEffectiveSystemProxyPortStr()

	if !wantEnable {
		g.cleanupRegistryProxyLocked(portStr)
		return
	}

	if !hasPort {
		slog.Warn("未检测到有效代理端口，已停用系统代理")
		g.disableInternalLocked()
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

	g.lastAppliedPort = portStr
}

func (g *ProxyGuard) cleanupRegistryProxyLocked(currentPortStr string) {
	targetPort := g.lastAppliedPort
	if targetPort == "" {
		targetPort = currentPortStr
	}

	if targetPort == "" {
		return
	}

	if err := sys.DisableProxy(targetPort); err != nil {
		slog.Error("注销系统代理失败", "port", targetPort, "err", err)
	} else {
		g.lastAppliedPort = ""
	}
}

func (g *ProxyGuard) disableInternal() {
	if g.st.IsExiting() {
		return
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	g.disableInternalLocked()
}

func (g *ProxyGuard) disableInternalLocked() {
	if g.cfg.IsSystemProxyEnabled() {
		g.cfg.Update(func(c *domain.TrayConfig) {
			b := false
			c.General.SystemProxy = &b
		})
	}

	portStr, _ := g.cfg.GetEffectiveSystemProxyPortStr()
	g.cleanupRegistryProxyLocked(portStr)
}

func (g *ProxyGuard) SafeShutdownCleanup() {
	g.mu.Lock()
	defer g.mu.Unlock()

	portStr, _ := g.cfg.GetEffectiveSystemProxyPortStr()
	g.cleanupRegistryProxyLocked(portStr)
	slog.Debug("系统代理退出清理完成")
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
			slog.Warn("检测到系统代理被外部接管，已停止", "server", status.Server, "expected", expectedServer)
			g.disableInternal()
			if g.onAlert != nil {
				g.onAlert("系统代理冲突", "代理设置被其他程序接管，已停止接管。")
			}
		}
		return
	}

	if !g.st.TryAcquireProxyRepair() {
		return
	}
	go g.reconcileDisconnection(ctx, expectedServer)
}

func (g *ProxyGuard) reconcileDisconnection(ctx context.Context, expectedServer string) {
	defer g.st.ReleaseProxyRepair()

	for attempt := 1; attempt <= proxyQuickReconcileMaxAttempts; attempt++ {
		select {
		case <-ctx.Done():
			return
		case <-time.After(proxyQuickReconcileInterval):
		}

		if g.st.IsExiting() || !g.cfg.IsSystemProxyEnabled() {
			return
		}

		cur, err := sys.GetProxyStatus()
		if err == nil && cur.Enabled && cur.Matches(expectedServer) {
			slog.Debug("系统代理状态已恢复正常", "attempt", attempt)
			return
		}

		g.Sync()

		cur, err = sys.GetProxyStatus()
		if err == nil && cur.Enabled && cur.Matches(expectedServer) {
			slog.Info("系统代理断线自愈成功", "attempt", attempt)
			return
		}
	}

	slog.Warn("系统代理多次被外部关闭，已停止接管")
	g.disableInternal()
	if g.onAlert != nil {
		g.onAlert("系统代理已关闭", "代理设置多次被外部关闭，已停止接管。")
	}
}
