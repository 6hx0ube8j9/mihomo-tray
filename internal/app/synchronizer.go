package app

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"
	
	"mihomo-tray/internal/config"
	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/sys"
)

const (
	TunInitGracePeriod = 20 * time.Second
	TunLostAlarmDelay  = 6 * time.Second
)

func (a *Application) getActualTunDevice() string {
	if dev := a.State.GetActualTunDevice(); dev != "" {
		return dev
	}
	return a.Cfg.Get("tun_device")
}

func (a *Application) isTunInGracePeriod() bool {
	reqTime := a.State.GetTunRequestedTime()
	lostTime := a.State.GetTunLostTime()

	reqActive := !reqTime.IsZero() && time.Since(reqTime) < TunInitGracePeriod
	lostActive := !lostTime.IsZero() && time.Since(lostTime) < TunLostAlarmDelay

	return reqActive || lostActive
}

func (a *Application) reconcileTunState(kernelTunEnabled bool) bool {
	wantTun := a.Cfg.Get(config.KeyTun) == "true"

	if wantTun && kernelTunEnabled && a.State.IsTunAlive() && a.isTunInGracePeriod() {
		a.State.SetTunRequestedTime(time.Time{})
		slog.Debug("TUN 接口就绪，解除保护")
	}

	if kernelTunEnabled != wantTun {
		if wantTun && !kernelTunEnabled && a.isTunInGracePeriod() {
			if time.Since(a.State.GetTunRequestedTime()) < TunInitGracePeriod {
				slog.Debug("TUN 保护期内，暂缓同步")
				return false
			}
		}

		slog.Info("TUN 状态外部变更", "expected", wantTun, "actual", kernelTunEnabled)
		a.Cfg.Set(config.KeyTun, fmt.Sprintf("%t", kernelTunEnabled))
		return true
	}
	return false
}

func (a *Application) syncSystemProxy() {
	enable := a.Cfg.Get(config.KeyProxy) == "true"
	port := a.Cfg.Get("port")
	if enable {
		slog.Info("系统代理配置已启用", "port", port)
	} else {
		slog.Info("系统代理配置已关闭")
	}
	if err := sys.SetSystemProxy(enable, port); err != nil {
		slog.Error("设置系统代理失败", "err", err)
	}
}

func (a *Application) handleProxyStatusChange(ctx context.Context, status sys.ProxyStatus) {
	if a.State.IsExiting() {
		return
	}

	expectedProxy := a.Cfg.Get(config.KeyProxy) == "true"
	expectedPort := a.Cfg.Get("port")
	expectedServer := "127.0.0.1:" + expectedPort

	if expectedProxy {
		if status.Enabled {
			if status.Server != "" && !strings.EqualFold(status.Server, expectedServer) {
				slog.Warn("代理被外部修改，关闭本地状态", "server", status.Server)
				a.Cfg.Set(config.KeyProxy, "false")
				a.pushUIState()
			}
			return
		}

		if !a.State.TryAcquireProxyRepair() {
			return
		}

		go func() {
			defer a.State.ReleaseProxyRepair()

			for i := 1; i <= 10; i++ {
				if a.State.IsExiting() || ctx.Err() != nil || a.Cfg.Get(config.KeyProxy) != "true" {
					return
				}
				a.syncSystemProxy()

				select {
				case <-ctx.Done():
					return
				case <-time.After(1000 * time.Millisecond):
				}

				cur, err := sys.GetProxyStatus()
				if err == nil && cur.Enabled && strings.EqualFold(cur.Server, expectedServer) {
					return
				}
			}
			a.Cfg.Set(config.KeyProxy, "false")
			a.pushUIState()
		}()
		return
	}
}

func (a *Application) handleTunChange(ctx context.Context) {
	if a.State.IsExiting() || a.State.IsConfigSyncing() {
		return
	}

	tunDev := a.getActualTunDevice()
	alive := sys.IsTunActive(tunDev)

	if a.State.IsTunAlive() != alive {
		slog.Info("TUN 网卡状态变更", "device", tunDev, "active", alive)
		a.State.SetTunAlive(alive)
		if !alive {
			a.State.SetTunLostTime(time.Now())
		}

		go func() {
			for i := 0; i < 3; i++ {
				select {
				case <-ctx.Done():
					return
				case <-time.After(300 * time.Millisecond):
				}
				select {
				case a.apiPollCh <- struct{}{}:
				default:
				}
			}
		}()
		a.pushUIState()
	}
}

func (a *Application) syncAllConfig(ctx context.Context) {
	if a.State.GetPhase() != domain.PhaseRunning {
		return
	}
	tunPayload := map[string]interface{}{"enable": a.Cfg.Get(config.KeyTun) == "true"}
	if dev := a.Cfg.Get("tun_device"); dev != "" {
		tunPayload["device"] = dev
	}
	payload := map[string]interface{}{
		"tun":  tunPayload,
		"mode": a.Cfg.Get(config.KeyMode),
	}
	_ = a.API.SyncConfigToKernel(ctx, payload)
}

func (a *Application) pollKernelAPI(ctx context.Context) bool {
	if a.State.IsExiting() || a.State.IsReloading() || a.State.IsConfigSyncing() {
		return false
	}

	queryCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()

	resp, err := a.API.GetKernelStatus(queryCtx)
	if err != nil {
		return false
	}

	changed := false
	currentActual := a.getActualTunDevice()
	
	if resp.Tun.Device != "" && resp.Tun.Device != currentActual {
		a.State.SetActualTunDevice(resp.Tun.Device)
		currentActual = resp.Tun.Device
		changed = true
	}
	
	realAlive := sys.IsTunActive(currentActual)
	if a.State.IsTunAlive() != realAlive {
		a.State.SetTunAlive(realAlive)
		changed = true
	}

	if resp.Mode != "" && resp.Mode != a.Cfg.Get(config.KeyMode) {
		slog.Info("内核路由模式已变更", "from", a.Cfg.Get(config.KeyMode), "to", resp.Mode)
		a.Cfg.Set(config.KeyMode, resp.Mode)
		changed = true
	}

	if a.reconcileTunState(resp.Tun.Enable) {
		changed = true
	}

	wantTun := a.Cfg.Get(config.KeyTun) == "true"
	if changed && wantTun && !realAlive && !a.isTunInGracePeriod() {
		slog.Warn("TUN 接口异常断开", "device", currentActual)
	}

	return changed
}
