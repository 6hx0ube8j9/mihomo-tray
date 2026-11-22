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

func (g *ProxyGuard) SetProxy(enable bool) error {
	if g.st.IsExiting() {
		return nil
	}

	if !enable {
		g.disableInternal()
		return nil
	}

	if g.st.GetPhase() != domain.PhaseRunning {
		return errKernelNotReady
	}

	portStr, hasPort := g.cfg.GetConfig().EffectiveSystemProxyPortStr()
	if !hasPort {
		return errNoProxyPort
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if err := sys.EnableSystemProxy(portStr); err != nil {
		return err
	}

	g.cfg.Update(func(c *domain.TrayConfig) {
		b := true
		c.General.SystemProxy = &b
	})
	g.lastAppliedPort = portStr
	return nil
}

func (g *ProxyGuard) Sync() {
	g.syncInternal()
}

func (g *ProxyGuard) syncInternal() bool {
	if g.st.IsExiting() {
		return false
	}

	g.mu.Lock()

	cfg := g.cfg.GetConfig()
	wantEnable := cfg.IsSystemProxyEnabled()
	portStr, hasPort := cfg.EffectiveSystemProxyPortStr()

	if !wantEnable {
		g.cleanupRegistryProxyLocked(portStr)
		g.mu.Unlock()
		return false
	}

	if !hasPort {
		slog.Warn("系统代理已停用", "err", errNoProxyPort)
		g.disableInternalLocked()
		g.mu.Unlock()
		if g.onAlert != nil {
			g.onAlert("系统代理已停用", errNoProxyPort.Error())
		}
		return false
	}

	if err := sys.EnableSystemProxy(portStr); err != nil {
		g.mu.Unlock()
		slog.Error("同步系统代理失败", "err", err)
		return false
	}

	g.lastAppliedPort = portStr
	g.mu.Unlock()
	return true
}

func (g *ProxyGuard) cleanupRegistryProxyLocked(currentPortStr string) {
	targetPort := g.lastAppliedPort
	if targetPort == "" {
		targetPort = currentPortStr
	}

	if targetPort == "" {
		return
	}

	if err := sys.DisableSystemProxy(targetPort); err != nil {
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
	cfg := g.cfg.GetConfig()
	if cfg.IsSystemProxyEnabled() {

		g.cfg.Update(func(c *domain.TrayConfig) {
			b := false
			c.General.SystemProxy = &b
		})
	}

	portStr, _ := cfg.EffectiveSystemProxyPortStr()
	g.cleanupRegistryProxyLocked(portStr)
}

func (g *ProxyGuard) SafeShutdownCleanup() {
	g.mu.Lock()
	defer g.mu.Unlock()

	portStr, _ := g.cfg.GetConfig().EffectiveSystemProxyPortStr()
	g.cleanupRegistryProxyLocked(portStr)
}

func (g *ProxyGuard) HandleStatusChange(ctx context.Context, status sys.ProxyStatus) {
	if g.st.IsExiting() {
		return
	}
	cfg := g.cfg.GetConfig()
	if !cfg.IsSystemProxyEnabled() {
		return
	}

	expectedPort, ok := cfg.EffectiveSystemProxyPortStr()
	if !ok {
		g.disableInternal()
		return
	}

	expectedServer := domain.LocalhostIP + ":" + expectedPort

	if status.Enabled {
		if status.Server != "" && !status.Matches(expectedServer) {
			slog.Warn("系统代理被外部接管", "server", status.Server, "expected", expectedServer)
			g.disableInternal()
			if g.onAlert != nil {
				g.onAlert("系统代理已变更", "设置被其他程序修改，已停止接管")
			}
		}
		return
	}

	if !g.st.TryAcquireProxyRepair() {
		return
	}
	go g.reconcileDisconnection(ctx, expectedServer)
}

func (g *ProxyGuard) isStateExpected(expectedServer string) bool {
	cur, err := sys.GetSystemProxy()
	return err == nil && cur.Enabled && cur.Matches(expectedServer)
}

func (g *ProxyGuard) reconcileDisconnection(ctx context.Context, expectedServer string) {
	defer g.st.ReleaseProxyRepair()

	timer := time.NewTimer(proxyQuickReconcileInterval)
	defer timer.Stop()

	for attempt := 1; attempt <= proxyQuickReconcileMaxAttempts; attempt++ {
		if attempt > 1 {
			timer.Reset(proxyQuickReconcileInterval)
		}

		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}

		if g.st.IsExiting() || !g.cfg.GetConfig().IsSystemProxyEnabled() {
			return
		}

		if g.isStateExpected(expectedServer) {
			return
		}

		if g.syncInternal() && g.isStateExpected(expectedServer) {
			slog.Info("系统代理已自愈", "attempt", attempt)
			return
		}
	}

	slog.Warn("系统代理被外部多次关闭，已停止接管")
	g.disableInternal()
	if g.onAlert != nil {
		g.onAlert("系统代理已变更", "设置多次被外部关闭，已停止接管")
	}
}
