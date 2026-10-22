package app

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"mihomo-tray/internal/config"
	"mihomo-tray/internal/core"
	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/netutil"
	"mihomo-tray/internal/state"
	"mihomo-tray/internal/sys"
)

const apiSyncTimeout = 3 * time.Second

type KernelTuner struct {
	cfg       *config.Manager
	st        *state.RuntimeState
	api       *core.APIClient
	forceSync func()
}

func NewKernelTuner(cfg *config.Manager, st *state.RuntimeState, api *core.APIClient, forceSync func()) *KernelTuner {
	return &KernelTuner{
		cfg:       cfg,
		st:        st,
		api:       api,
		forceSync: forceSync,
	}
}

func (t *KernelTuner) triggerForceSync() {
	if t.forceSync != nil {
		t.forceSync()
	}
}

func (t *KernelTuner) syncAPI(ctx context.Context, action string, call func(context.Context) error, commit func()) error {
	if t.st.GetPhase() != domain.PhaseRunning {
		return domain.ErrKernelNotReady
	}

	if !t.st.TryBeginAction(state.ActionSyncAPI) {
		return domain.ErrSystemBusy
	}
	defer func() {
		t.st.EndAction()
		t.triggerForceSync()
	}()

	reqCtx, cancel := context.WithTimeout(ctx, apiSyncTimeout)
	defer cancel()

	if err := call(reqCtx); err != nil {
		if ctx.Err() == nil && (errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)) {
			slog.Warn("下发参数响应超时，由后台探针继续同步", "action", action)
		} else {
			return err
		}
	}

	if commit != nil {
		commit()
	}
	return nil
}

func (t *KernelTuner) ToggleTun(ctx context.Context, enable bool) error {
	if t.cfg.GetConfig().Config.Tun.Enable == enable {
		return nil
	}

	if enable {
		targetDev := t.st.GetActualTunDevice()
		if targetDev == "" {
			targetDev = domain.DefaultTunDevice
		}

		if sys.IsTunActive(targetDev) {
			slog.Warn("虚拟网卡已被占用，无法开启 TUN", "device", targetDev)
			return domain.ErrTunDeviceBusy
		}
	}

	return t.syncAPI(ctx, "切换 TUN 模式", func(reqCtx context.Context) error {
		return t.api.UpdateTun(reqCtx, enable, t.st.GetActualTunDevice())
	}, func() {
		t.cfg.Update(func(c *domain.TrayConfig) { c.Config.Tun.Enable = enable })
		if enable {
			t.st.SetTunRequestedTime(time.Now())
		} else {
			t.st.SetTunRequestedTime(time.Time{})
		}
	})
}

func (t *KernelTuner) SwitchMode(ctx context.Context, mode string) error {
	if mode == t.cfg.GetConfig().Config.Mode {
		return nil
	}

	return t.syncAPI(ctx, "切换运行模式", func(reqCtx context.Context) error {
		return t.api.UpdateMode(reqCtx, mode)
	}, func() {
		t.cfg.Update(func(c *domain.TrayConfig) { c.Config.Mode = mode })
	})
}

func (t *KernelTuner) ToggleAllowLan(ctx context.Context, enable bool) error {
	if enable == t.cfg.GetEffectiveAllowLan() {
		return nil
	}

	return t.syncAPI(ctx, "切换局域网共享", func(reqCtx context.Context) error {
		return t.api.UpdateAllowLan(reqCtx, enable)
	}, func() {
		t.cfg.Update(func(c *domain.TrayConfig) {
			b := enable
			c.Config.AllowLan = &b
		})
	})
}

func (t *KernelTuner) GetPortSnapshot() (mixed, socks, httpPort int) {
	cfg := t.cfg.GetConfig()
	mixed = t.cfg.GetEffectiveMixedPort()
	socks = t.cfg.GetEffectivePort(cfg.Config.SocksPort, domain.DefaultSocksPort)
	httpPort = t.cfg.GetEffectivePort(cfg.Config.Port, domain.DefaultPort)
	return
}

func (t *KernelTuner) ApplyPortConfig(ctx context.Context, mixed, socks, httpPort int) (bool, error) {
	if !netutil.IsValidPort(mixed) || !netutil.IsValidPort(socks) || !netutil.IsValidPort(httpPort) {
		return false, domain.ErrInvalidPort
	}

	cMixed, cSocks, cHttp := t.GetPortSnapshot()
	if mixed == cMixed && socks == cSocks && httpPort == cHttp {
		return false, nil
	}

	commit := func() {
		t.cfg.Update(func(c *domain.TrayConfig) {
			m, sk, h := mixed, socks, httpPort
			c.Config.MixedPort = &m
			c.Config.SocksPort = &sk
			c.Config.Port = &h
		})
	}

	if t.st.GetPhase() != domain.PhaseRunning {
		commit()
		return true, nil
	}

	err := t.syncAPI(ctx, "修改监听端口", func(reqCtx context.Context) error {
		return t.api.UpdatePorts(reqCtx, mixed, socks, httpPort)
	}, commit)
	if err != nil {
		return false, err
	}

	return true, nil
}

func (t *KernelTuner) ReconcilePrivilege() {
	if sys.IsAdmin() {
		return
	}
	if t.cfg.GetConfig().Config.Tun.Enable {
		slog.Warn("当前无管理员权限，自动停用 TUN 模式")
		t.cfg.Update(func(c *domain.TrayConfig) {
			c.Config.Tun.Enable = false
		})
	}
}
