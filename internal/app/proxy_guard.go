package app

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/sys"
)

const (
	proxyRepairMaxAttempts  = 10
	proxyRepairPollInterval = 1000 * time.Millisecond
)

func (a *Application) ToggleProxy(enable bool) {
	a.Cfg.Update(func(c *domain.TrayConfig) {
		b := enable
		c.General.SystemProxy = &b
	})
	a.syncSystemProxy()
}

func (a *Application) syncSystemProxy() {
	wantEnable := a.Cfg.IsSystemProxyEnabled()

	if !wantEnable {
		if err := sys.SetSystemProxy(false, ""); err != nil {
			slog.Error("关闭系统代理失败", "err", err)
		}
		return
	}

	portStr, hasPort := a.Cfg.GetEffectiveSystemProxyPortStr()
	if !hasPort {
		slog.Warn("Mixed 与 HTTP 端口均未开启，强制关闭系统代理")
		a.disableSystemProxySetting()

		if a.ui != nil {
			a.ui.ShowNotification("系统代理", "系统代理已关闭，请检查代理端口配置。")
		}
		return
	}

	slog.Debug("更新系统代理", "enabled", true, "port", portStr)
	if err := sys.SetSystemProxy(true, portStr); err != nil {
		slog.Error("设置系统代理失败", "err", err)
	}
}

func (a *Application) handleProxyStatusChange(ctx context.Context, status sys.ProxyStatus) {
	if a.State.IsExiting() || !a.Cfg.IsSystemProxyEnabled() {
		return
	}

	expectedPort, ok := a.Cfg.GetEffectiveSystemProxyPortStr()
	if !ok {
		a.disableSystemProxySetting()
		return
	}

	expectedServer := domain.LocalhostIP + ":" + expectedPort

	if status.Enabled {
		if status.Server != "" && !strings.EqualFold(status.Server, expectedServer) {
			slog.Warn("系统代理被外部修改，停用本地代理接管", "server", status.Server, "expected", expectedServer)
			a.disableSystemProxySetting()
		}
		return
	}
  
	if !a.State.TryAcquireProxyRepair() {
		return
	}

	go a.repairProxyLoop(ctx, expectedPort, expectedServer)
}

func (a *Application) repairProxyLoop(ctx context.Context, port, expectedServer string) {
	defer a.State.ReleaseProxyRepair()

	for i := 1; i <= proxyRepairMaxAttempts; i++ {
		if a.State.IsExiting() || ctx.Err() != nil || !a.Cfg.IsSystemProxyEnabled() {
			return
		}

		a.syncSystemProxy()

		select {
		case <-ctx.Done():
			return
		case <-time.After(proxyRepairPollInterval):
		}

		cur, err := sys.GetProxyStatus()
		if err == nil && cur.Enabled && strings.EqualFold(cur.Server, expectedServer) {
			slog.Debug("系统代理已自动恢复", "attempt", i)
			return
		}
	}

	slog.Warn("系统代理持续被外部修改，已自动停用")
	a.disableSystemProxySetting()
}

func (a *Application) disableSystemProxySetting() {
	if a.State.IsExiting() {
		return
	}
	a.Cfg.Update(func(c *domain.TrayConfig) {
		b := false
		c.General.SystemProxy = &b
	})

	if err := sys.SetSystemProxy(false, ""); err != nil {
		slog.Error("注销系统代理注册表失败", "err", err)
	}
	a.pushUIState()
}
