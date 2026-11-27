package app

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"mihomo-tray/internal/config"
	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/state"
	"mihomo-tray/internal/webui"
)

type WebUIService struct {
	cfg     *config.Manager
	st      *state.RuntimeState
	coreMgr *webui.Manager

	reopenMu     sync.Mutex
	reopenCancel context.CancelFunc
	reopenGen    uint64
}

func NewWebUIService(cfg *config.Manager, st *state.RuntimeState, coreMgr *webui.Manager) *WebUIService {
	return &WebUIService{
		cfg:     cfg,
		st:      st,
		coreMgr: coreMgr,
	}
}

func (s *WebUIService) Open(forceSync func(), onReady func()) error {
	if s.st.GetPhase() != domain.PhaseRunning {
		if onReady != nil {
			onReady()
		}
		return errKernelNotReady
	}

	cfg := s.cfg.GetConfig()
	ctrl := cfg.ControllerSettings()
	proxyPort, _ := cfg.EffectiveSystemProxyPortStr()

	wcfg := webui.Config{
		APIAddr:              ctrl.Addr,
		Secret:               ctrl.Secret,
		ProxyPort:            proxyPort,
		BaseDir:              s.cfg.BaseDir(),
		UIName:               cfg.Config.ExternalUIName,
		WebOpenMode:          ctrl.WebOpenMode,
		RemoteWebUI:          ctrl.RemoteWebUI,
		RemoteWebUIURL:       ctrl.RemoteWebUIURL,
		CustomBrowserEnabled: ctrl.CustomBrowserEnabled,
		CustomBrowserPath:    ctrl.CustomBrowserPath,
		IsDark:               s.st.GetSystemTheme().IsDark(),
	}

	if forceSync != nil {
		forceSync()
	}

	return s.coreMgr.Launch(wcfg, onReady)
}

func (s *WebUIService) ReloadIfActive() {
	if !s.coreMgr.IsActive() {
		return
	}

	reloadNow := func() {
		if !s.coreMgr.IsActive() {
			return
		}
		if err := s.coreMgr.Reload(); err != nil {
			slog.Warn("刷新 Web 面板失败", "err", err)
		}
	}

	if s.st.GetPhase() == domain.PhaseRunning {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("刷新 Web 面板异常", "err", r)
				}
			}()
			reloadNow()
		}()
		return
	}

	s.scheduleAfterKernelReady("刷新 Web 面板", reloadNow)
}

func (s *WebUIService) ReopenIfActive(forceSync func()) {
	if !s.coreMgr.IsActive() {
		return
	}
	s.coreMgr.Cleanup()

	s.scheduleAfterKernelReady("恢复 Web 面板", func() {
		if err := s.Open(forceSync, nil); err != nil {
			slog.Warn("恢复 Web 面板失败", "err", err)
		}
	})
}

func (s *WebUIService) scheduleAfterKernelReady(actionName string, onReady func()) {
	s.reopenMu.Lock()
	if s.reopenCancel != nil {
		s.reopenCancel()
	}
	s.reopenGen++
	gen := s.reopenGen
	ctx, cancel := context.WithCancel(context.Background())
	s.reopenCancel = cancel
	s.reopenMu.Unlock()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error(actionName+"异常", "err", r)
			}
			cancel()
			s.reopenMu.Lock()
			if s.reopenGen == gen {
				s.reopenCancel = nil
			}
			s.reopenMu.Unlock()
		}()

		if s.st.IsExiting() {
			return
		}
		if s.st.GetPhase() == domain.PhaseRunning {
			onReady()
			return
		}

		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()

		for i := 0; i < 60; i++ {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if s.st.IsExiting() {
					return
				}
				if s.st.GetPhase() == domain.PhaseRunning {
					onReady()
					return
				}
			}
		}
		slog.Warn("等待内核就绪超时，未"+actionName)
	}()
}

func (s *WebUIService) ClearCache() error {
	return s.coreMgr.ClearCache(s.cfg.BaseDir())
}
