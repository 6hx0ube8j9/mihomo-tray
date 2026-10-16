package app

import (
	"context"
	"errors"
	"fmt"
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
	deployer  *Deployer
	proxy     *ProxyGuard
	presenter *Presenter
}

func NewTuner(cfg *config.Manager, st *state.RuntimeState, api *core.APIClient, deployer *Deployer, proxy *ProxyGuard, presenter *Presenter) *Tuner {
	return &Tuner{
		cfg:       cfg,
		st:        st,
		api:       api,
		deployer:  deployer,
		proxy:     proxy,
		presenter: presenter,
	}
}

func (t *Tuner) ToggleProxy(enable bool) {
	t.proxy.SetProxy(enable)
}

func (t *Tuner) ToggleTun(ctx context.Context, enable bool, elevate func(rollback func()) bool) (restarted bool, err error) {
	originalTun := t.cfg.GetConfig().Config.Tun.Enable
	if originalTun == enable {
		return false, nil
	}

	rollback := func() {
		t.cfg.Update(func(c *domain.TrayConfig) { c.Config.Tun.Enable = originalTun })
		if enable {
			t.st.SetTunRequestedTime(time.Time{})
		}
	}

	t.cfg.Update(func(c *domain.TrayConfig) { c.Config.Tun.Enable = enable })
	if enable {
		t.st.SetTunRequestedTime(time.Now())
	}

	restarted = elevate(rollback)
	if restarted {
		return true, nil
	}

	if t.st.GetPhase() != domain.PhaseRunning {
		rollback()
		return false, ErrKernelNotReady
	}

	if !t.st.TryBeginAction(state.ActionSyncAPI) {
		rollback()
		return false, ErrSystemBusy
	}
	defer t.st.EndAction()

	reqCtx, cancel := context.WithTimeout(ctx, apiSyncTimeout)
	defer cancel()

	if syncErr := t.api.UpdateTun(reqCtx, enable, t.st.GetActualTunDevice()); syncErr != nil {
		rollback()
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

func (t *Tuner) ApplyPortConfig(ctx context.Context, mixed, socks, httpPort int) error {
	if !netutil.IsValidPort(mixed) || !netutil.IsValidPort(socks) || !netutil.IsValidPort(httpPort) {
		return ErrInvalidPort
	}
	cMixed, cSocks, cHttp := t.GetPortSnapshot()
	if mixed == cMixed && socks == cSocks && httpPort == cHttp {
		return nil
	}

	t.cfg.Update(func(c *domain.TrayConfig) {
		m, sk, h := mixed, socks, httpPort
		c.Config.MixedPort = &m
		c.Config.SocksPort = &sk
		c.Config.Port = &h
	})
	t.presenter.PushUIState()

	if t.cfg.IsSystemProxyEnabled() {
		t.proxy.Sync()
	}

	if t.st.GetPhase() == domain.PhaseRunning {
		if !t.st.TryBeginAction(state.ActionSyncAPI) {
			return ErrSystemBusy
		}
		defer t.st.EndAction()

		reqCtx, cancel := context.WithTimeout(ctx, apiSyncTimeout)
		defer cancel()

		if err := t.api.UpdatePorts(reqCtx, mixed, socks, httpPort); err != nil {
			if ctx.Err() == nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
				return err
			}
		}
	}
	return nil
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

func (t *Tuner) ApplyControllerConfig(ctx context.Context, addr, sec string, online, sys bool, remoteURL string) (restarted bool, err error) {
	cAddr, cSec, cOnline, cSys, cRemoteURL := t.GetControllerSnapshot()
	coreChanged := (cAddr != addr) || (cSec != sec)
	appChanged := (cOnline != online) || (cSys != sys) || (cRemoteURL != remoteURL)

	if !coreChanged && !appChanged {
		return false, nil
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
	t.presenter.PushUIState()

	if coreChanged {
		if err := t.deployer.RestartKernel(ctx); err != nil {
			return false, fmt.Errorf("重启内核失败: %w", err)
		}
		return true, nil
	}
	return false, nil
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
