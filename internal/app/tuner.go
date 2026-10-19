package app

import (
	"context"
	"errors"
	"time"

	"mihomo-tray/internal/config"
	"mihomo-tray/internal/core"
	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/netutil"
	"mihomo-tray/internal/state"
)

const apiSyncTimeout = 2 * time.Second

var (
	ErrKernelNotReady = errors.New("内核尚未就绪")
	ErrSystemBusy     = errors.New("系统繁忙，请稍后重试")
	ErrInvalidPort    = errors.New("端口超出合法范围 (0-65535)")
	ErrAPITimeout     = errors.New("与内核通信超时")
)

type Tuner struct {
	cfg       *config.Manager
	st        *state.RuntimeState
	api       *core.APIClient
	forceSync func()
}

func NewTuner(cfg *config.Manager, st *state.RuntimeState, api *core.APIClient, forceSync func()) *Tuner {
	return &Tuner{
		cfg:       cfg,
		st:        st,
		api:       api,
		forceSync: forceSync,
	}
}

func (t *Tuner) triggerForceSync() {
	if t.forceSync != nil {
		t.forceSync()
	}
}

func (t *Tuner) ToggleTun(ctx context.Context, enable bool, elevate func(rollback func()) bool) (restarted bool, err error) {
	originalTun := t.cfg.GetConfig().Config.Tun.Enable
	if originalTun == enable {
		return false, nil
	}

	rollbackConfig := func() {
		t.cfg.Update(func(c *domain.TrayConfig) { c.Config.Tun.Enable = originalTun })
		if enable {
			t.st.SetTunRequestedTime(time.Time{})
		}
	}

	t.cfg.Update(func(c *domain.TrayConfig) { c.Config.Tun.Enable = enable })
	if enable {
		t.st.SetTunRequestedTime(time.Now())
	}

	restarted = elevate(rollbackConfig)
	if restarted {
		return true, nil
	}

	if t.st.GetPhase() != domain.PhaseRunning {
		rollbackConfig()
		return false, ErrKernelNotReady
	}

	if !t.st.TryBeginAction(state.ActionSyncAPI) {
		rollbackConfig()
		return false, ErrSystemBusy
	}
	defer t.st.EndAction()
	defer t.triggerForceSync()

	reqCtx, cancel := context.WithTimeout(ctx, apiSyncTimeout)
	defer cancel()

	if syncErr := t.api.UpdateTun(reqCtx, enable, t.st.GetActualTunDevice()); syncErr != nil {
		if ctx.Err() == nil {
			if errors.Is(syncErr, context.DeadlineExceeded) || errors.Is(syncErr, context.Canceled) {
				return false, ErrAPITimeout
			}
			return false, syncErr
		}
	}
	return false, nil
}

func (t *Tuner) SwitchMode(ctx context.Context, mode string) error {
	if t.st.GetPhase() != domain.PhaseRunning {
		return ErrKernelNotReady
	}
	if mode == t.cfg.GetConfig().Config.Mode {
		return nil
	}
	if !t.st.TryBeginAction(state.ActionSyncAPI) {
		return ErrSystemBusy
	}
	defer t.st.EndAction()
	defer t.triggerForceSync()

	reqCtx, cancel := context.WithTimeout(ctx, apiSyncTimeout)
	defer cancel()

	if err := t.api.UpdateMode(reqCtx, mode); err != nil {
		return err
	}
	t.cfg.Update(func(c *domain.TrayConfig) { c.Config.Mode = mode })
	return nil
}

func (t *Tuner) ToggleAllowLan(ctx context.Context, enable bool) error {
	if t.st.GetPhase() != domain.PhaseRunning {
		return ErrKernelNotReady
	}
	if enable == t.cfg.GetEffectiveAllowLan() {
		return nil
	}
	if !t.st.TryBeginAction(state.ActionSyncAPI) {
		return ErrSystemBusy
	}
	defer t.st.EndAction()
	defer t.triggerForceSync()

	reqCtx, cancel := context.WithTimeout(ctx, apiSyncTimeout)
	defer cancel()

	if err := t.api.UpdateAllowLan(reqCtx, enable); err != nil {
		return err
	}
	t.cfg.Update(func(c *domain.TrayConfig) {
		b := enable
		c.Config.AllowLan = &b
	})
	return nil
}

func (t *Tuner) GetPortSnapshot() (mixed, socks, httpPort int) {
	cfg := t.cfg.GetConfig()
	mixed = t.cfg.GetEffectiveMixedPort()
	socks = t.cfg.GetEffectivePort(cfg.Config.SocksPort, domain.DefaultSocksPort)
	httpPort = t.cfg.GetEffectivePort(cfg.Config.Port, domain.DefaultPort)
	return
}

func (t *Tuner) ApplyPortConfig(ctx context.Context, mixed, socks, httpPort int) (changed bool, err error) {
	if !netutil.IsValidPort(mixed) || !netutil.IsValidPort(socks) || !netutil.IsValidPort(httpPort) {
		return false, ErrInvalidPort
	}
	cMixed, cSocks, cHttp := t.GetPortSnapshot()
	if mixed == cMixed && socks == cSocks && httpPort == cHttp {
		return false, nil
	}

	t.cfg.Update(func(c *domain.TrayConfig) {
		m, sk, h := mixed, socks, httpPort
		c.Config.MixedPort = &m
		c.Config.SocksPort = &sk
		c.Config.Port = &h
	})

	if t.st.GetPhase() == domain.PhaseRunning {
		if !t.st.TryBeginAction(state.ActionSyncAPI) {
			return true, ErrSystemBusy
		}
		defer t.st.EndAction()
		defer t.triggerForceSync()

		reqCtx, cancel := context.WithTimeout(ctx, apiSyncTimeout)
		defer cancel()

		if err := t.api.UpdatePorts(reqCtx, mixed, socks, httpPort); err != nil {
			if ctx.Err() == nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
				return true, err
			}
		}
	}
	return true, nil
}

func (t *Tuner) GetControllerSnapshot() (addr, sec string, online, sys bool, remoteURL string) {
	cfg := t.cfg.GetConfig()
	addr = cfg.Config.ExternalController
	if addr == "" {
		addr = domain.DefaultExternalController
	}
	sec = t.cfg.GetEffectiveSecret(cfg.Config.Secret)
	online = t.cfg.IsRemoteWebUIEnabled()
	sys = t.cfg.IsSystemBrowserEnabled()
	if cfg.General.RemoteWebUIURL != nil {
		remoteURL = *cfg.General.RemoteWebUIURL
	}
	return
}

func (t *Tuner) ApplyControllerConfig(addr, sec string, online, sys bool, remoteURL string) (coreChanged, appChanged bool) {
	cAddr, cSec, cOnline, cSys, cRemoteURL := t.GetControllerSnapshot()
	coreChanged = (cAddr != addr) || (cSec != sec)
	appChanged = (cOnline != online) || (cSys != sys) || (cRemoteURL != remoteURL)

	if !coreChanged && !appChanged {
		return false, false
	}

	t.cfg.Update(func(c *domain.TrayConfig) {
		if coreChanged {
			c.Config.ExternalController = addr
			c.Config.Secret = &sec
		}
		bOnline, bSys, bRemoteURL := online, sys, remoteURL
		c.General.RemoteWebUI = &bOnline
		c.General.SystemBrowser = &bSys
		c.General.RemoteWebUIURL = &bRemoteURL
	})
	return coreChanged, appChanged
}

func (t *Tuner) ToggleSystemBrowser(enable bool) {
	t.cfg.Update(func(c *domain.TrayConfig) {
		b := enable
		c.General.SystemBrowser = &b
	})
}

func (t *Tuner) ToggleRemoteWebUI(enable bool) {
	t.cfg.Update(func(c *domain.TrayConfig) {
		b := enable
		c.General.RemoteWebUI = &b
	})
}
