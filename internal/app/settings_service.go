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

type SettingsService struct {
	cfg       *config.Manager
	st        *state.RuntimeState
	api       *core.APIClient
	deployer  *Deployer
	proxy     *ProxyGuard
	presenter *Presenter
}

func NewSettingsService(cfg *config.Manager, st *state.RuntimeState, api *core.APIClient, deployer *Deployer, proxy *ProxyGuard, presenter *Presenter) *SettingsService {
	return &SettingsService{
		cfg:       cfg,
		st:        st,
		api:       api,
		deployer:  deployer,
		proxy:     proxy,
		presenter: presenter,
	}
}

func (s *SettingsService) ToggleProxy(enable bool) {
	s.proxy.SetProxy(enable)
}

func (s *SettingsService) ToggleTun(ctx context.Context, enable bool) (restarted bool, err error) {
	originalTun := s.cfg.GetConfig().Config.Tun.Enable
	if originalTun == enable {
		return false, nil
	}

	rollback := func() {
		s.cfg.Update(func(c *domain.TrayConfig) { c.Config.Tun.Enable = originalTun })
		if enable {
			s.st.SetTunRequestedTime(time.Time{})
		}
	}

	s.cfg.Update(func(c *domain.TrayConfig) { c.Config.Tun.Enable = enable })
	if enable {
		s.st.SetTunRequestedTime(time.Now())
	}

	if s.st.GetPhase() != domain.PhaseRunning {
		rollback()
		return false, ErrKernelNotReady
	}

	if !s.st.TryBeginAction(state.ActionSyncAPI) {
		rollback()
		return false, ErrSystemBusy
	}
	defer s.st.EndAction()

	reqCtx, cancel := context.WithTimeout(ctx, apiSyncTimeout)
	defer cancel()

	if syncErr := s.api.UpdateTun(reqCtx, enable, s.st.GetActualTunDevice()); syncErr != nil {
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

func (s *SettingsService) SwitchMode(ctx context.Context, mode string) error {
	if s.st.GetPhase() != domain.PhaseRunning {
		return ErrKernelNotReady
	}
	if mode == s.cfg.GetConfig().Config.Mode {
		return nil
	}
	if !s.st.TryBeginAction(state.ActionSyncAPI) {
		return ErrSystemBusy
	}
	defer s.st.EndAction()

	reqCtx, cancel := context.WithTimeout(ctx, apiSyncTimeout)
	defer cancel()

	if err := s.api.UpdateMode(reqCtx, mode); err != nil {
		return err
	}
	s.cfg.Update(func(c *domain.TrayConfig) { c.Config.Mode = mode })
	return nil
}

func (s *SettingsService) ToggleAllowLan(ctx context.Context, enable bool) error {
	if s.st.GetPhase() != domain.PhaseRunning {
		return ErrKernelNotReady
	}
	if enable == s.cfg.GetEffectiveAllowLan() {
		return nil
	}
	if !s.st.TryBeginAction(state.ActionSyncAPI) {
		return ErrSystemBusy
	}
	defer s.st.EndAction()

	reqCtx, cancel := context.WithTimeout(ctx, apiSyncTimeout)
	defer cancel()

	if err := s.api.UpdateAllowLan(reqCtx, enable); err != nil {
		return err
	}
	s.cfg.Update(func(c *domain.TrayConfig) {
		b := enable
		c.Config.AllowLan = &b
	})
	return nil
}

func (s *SettingsService) GetPortSnapshot() (mixed, socks, httpPort int) {
	cfg := s.cfg.GetConfig()
	mixed = s.cfg.GetEffectiveMixedPort()
	socks = s.cfg.GetEffectivePort(cfg.Config.SocksPort, domain.DefaultSocksPort)
	httpPort = s.cfg.GetEffectivePort(cfg.Config.Port, domain.DefaultPort)
	return
}

func (s *SettingsService) ApplyPortConfig(ctx context.Context, mixed, socks, httpPort int) error {
	if !netutil.IsValidPort(mixed) || !netutil.IsValidPort(socks) || !netutil.IsValidPort(httpPort) {
		return ErrInvalidPort
	}
	cMixed, cSocks, cHttp := s.GetPortSnapshot()
	if mixed == cMixed && socks == cSocks && httpPort == cHttp {
		return nil
	}

	s.cfg.Update(func(c *domain.TrayConfig) {
		m, sk, h := mixed, socks, httpPort
		c.Config.MixedPort = &m
		c.Config.SocksPort = &sk
		c.Config.Port = &h
	})
	s.presenter.PushUIState()

	if s.cfg.IsSystemProxyEnabled() {
		s.proxy.Sync()
	}

	if s.st.GetPhase() == domain.PhaseRunning {
		if !s.st.TryBeginAction(state.ActionSyncAPI) {
			return ErrSystemBusy
		}
		defer s.st.EndAction()

		reqCtx, cancel := context.WithTimeout(ctx, apiSyncTimeout)
		defer cancel()

		if err := s.api.UpdatePorts(reqCtx, mixed, socks, httpPort); err != nil {
			if ctx.Err() == nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
				return err
			}
		}
	}
	return nil
}

func (s *SettingsService) GetControllerSnapshot() (addr, sec string, online, sys bool, remoteURL string) {
	cfg := s.cfg.GetConfig()
	addr = cfg.Config.ExternalController
	if addr == "" {
		addr = domain.DefaultExternalController
	}
	sec = s.cfg.GetEffectiveSecret(cfg.Config.Secret)
	online = s.cfg.IsRemoteWebUIEnabled()
	sys = s.cfg.IsSystemBrowserEnabled()
	if cfg.General.RemoteWebUIURL != nil {
		remoteURL = *cfg.General.RemoteWebUIURL
	}
	return
}

func (s *SettingsService) ApplyControllerConfig(ctx context.Context, addr, sec string, online, sys bool, remoteURL string) (restarted bool, err error) {
	cAddr, cSec, cOnline, cSys, cRemoteURL := s.GetControllerSnapshot()
	coreChanged := (cAddr != addr) || (cSec != sec)
	appChanged := (cOnline != online) || (cSys != sys) || (cRemoteURL != remoteURL)

	if !coreChanged && !appChanged {
		return false, nil
	}

	s.cfg.Update(func(c *domain.TrayConfig) {
		if coreChanged {
			c.Config.ExternalController = addr
			c.Config.Secret = &sec
		}
		bOnline, bSys, bRemoteURL := online, sys, remoteURL
		c.General.RemoteWebUI = &bOnline
		c.General.SystemBrowser = &bSys
		c.General.RemoteWebUIURL = &bRemoteURL
	})
	s.presenter.PushUIState()

	if coreChanged {
		if err := s.deployer.RestartKernel(ctx); err != nil {
			return false, fmt.Errorf("重启内核失败: %w", err)
		}
		return true, nil
	}
	return false, nil
}

func (s *SettingsService) ToggleSystemBrowser(enable bool) {
	s.cfg.Update(func(c *domain.TrayConfig) {
		b := enable
		c.General.SystemBrowser = &b
	})
}

func (s *SettingsService) ToggleRemoteWebUI(enable bool) {
	s.cfg.Update(func(c *domain.TrayConfig) {
		b := enable
		c.General.RemoteWebUI = &b
	})
}
