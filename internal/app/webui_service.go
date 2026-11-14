package app

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
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
	eventCh chan webui.Event

	reopenMu     sync.Mutex
	reopenCancel context.CancelFunc
}

func NewWebUIService(cfg *config.Manager, st *state.RuntimeState, coreMgr *webui.Manager, eventCh chan webui.Event) *WebUIService {
	return &WebUIService{
		cfg:     cfg,
		st:      st,
		coreMgr: coreMgr,
		eventCh: eventCh,
	}
}

func (s *WebUIService) Open(forceSync func(), onReady func()) error {
	if s.st.GetPhase() != domain.PhaseRunning {
		if onReady != nil {
			onReady()
		}
		return domain.ErrKernelNotReady
	}

	cfg := s.cfg.GetConfig()
	ctrl := s.cfg.GetControllerSettings()

	slog.Debug("启动 Web 面板",
		"addr", ctrl.Addr,
		"mode", ctrl.WebOpenMode,
		"remote", ctrl.RemoteWebUI,
		"customBrowser", ctrl.CustomBrowserEnabled,
	)

	currentTheme := s.st.GetSystemTheme()

	wcfg := webui.Config{
		APIAddr:              ctrl.Addr,
		Secret:               ctrl.Secret,
		ProxyPort:            s.cfg.GetEffectiveLocalProxyPortStr(),
		BaseDir:              s.cfg.BaseDir(),
		UIName:               cfg.Config.ExternalUIName,
		WebOpenMode:          ctrl.WebOpenMode,
		RemoteWebUI:          ctrl.RemoteWebUI,
		RemoteWebUIURL:       ctrl.RemoteWebUIURL,
		CustomBrowserEnabled: ctrl.CustomBrowserEnabled,
		CustomBrowserPath:    ctrl.CustomBrowserPath,
		IsDark:               currentTheme.IsDark(),
	}

	if forceSync != nil {
		forceSync()
	}

	defer func() {
		if r := recover(); r != nil {
			slog.Error("Web 面板启动异常拦截", "panic", r)
		}
	}()

	s.coreMgr.Launch(wcfg, s.eventCh, onReady)

	return nil
}

func (s *WebUIService) ReopenIfActive(forceSync func()) {
	if !s.coreMgr.IsActive() {
		return
	}
	s.coreMgr.Cleanup()

	s.reopenMu.Lock()
	if s.reopenCancel != nil {
		s.reopenCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.reopenCancel = cancel
	s.reopenMu.Unlock()

	go func() {
		defer func() {
			s.reopenMu.Lock()
			s.reopenCancel = nil
			s.reopenMu.Unlock()
		}()

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
					_ = s.Open(forceSync, nil)
					return
				}
			}
		}
		slog.Warn("等待内核就绪超时，未恢复 Web 面板")
	}()
}

func (s *WebUIService) ClearCache() error {
	unlock, ok := s.coreMgr.TryAcquireMaintenance()
	if !ok {
		return errors.New("面板正在运行或启动中，请先关闭")
	}
	defer unlock()

	cacheDir := filepath.Join(s.cfg.BaseDir(), domain.WebCacheDir)
	if _, err := os.Stat(cacheDir); os.IsNotExist(err) {
		return nil
	}

	var lastErr error
	for i := 0; i < 3; i++ {
		err := os.RemoveAll(cacheDir)
		if err == nil {
			return nil
		}
		lastErr = err
		time.Sleep(150 * time.Millisecond)
	}

	slog.Debug("清理面板缓存失败", "path", cacheDir, "err", lastErr)
	return errors.New("缓存文件被占用，请稍后重试")
}