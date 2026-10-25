package app

import (
	"context"
	"errors"
	"fmt"
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

func (s *WebUIService) Open(forceSync func()) error {
	if s.st.GetPhase() != domain.PhaseRunning {
		return errors.New("内核尚未就绪，请稍候")
	}

	cfg := s.cfg.GetConfig()
	addr := s.cfg.GetEffectiveExternalController()
	secret := s.cfg.GetEffectiveSecret(cfg.Config.Secret)
	online := s.cfg.IsRemoteWebUIEnabled()
	sysBrowser := s.cfg.IsSystemBrowserEnabled()
	remoteURL := s.cfg.GetEffectiveRemoteWebUIURL()

	slog.Info("启动 Web 面板", "addr", addr, "sys_browser", sysBrowser, "remote", online)

	wcfg := webui.Config{
		APIAddr:            addr,
		Secret:             secret,
		ProxyPort:          s.cfg.GetEffectiveLocalProxyPortStr(),
		BaseDir:            s.cfg.BaseDir(),
		UIName:             cfg.Config.ExternalUIName,
		ForceSystemBrowser: sysBrowser,
		RemoteWebUI:        online,
		RemoteWebUIURL:     remoteURL,
		SystemTheme:        currentTheme,
	}

	if forceSync != nil {
		forceSync()
	}

	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("Web 面板启动异常拦截", "panic", r)
			}
		}()
		s.coreMgr.Launch(wcfg, s.eventCh)
	}()

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
					_ = s.Open(forceSync)
					return
				}
			}
		}
		slog.Warn("等待内核就绪超时，未恢复 Web 面板")
	}()
}

func (s *WebUIService) ClearCache() error {
	if s.coreMgr.IsActive() {
		return errors.New("请先关闭正在运行的 Web 面板窗口")
	}

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
	return fmt.Errorf("缓存文件被占用，请稍后重试: %w", lastErr)
}
