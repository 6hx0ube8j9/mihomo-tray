package app

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"mihomo-tray/internal/config"
	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/state"
	"mihomo-tray/internal/sys"
	"mihomo-tray/internal/webui"
)

type Panel struct {
	cfg     *config.Manager
	st      *state.RuntimeState
	coreMgr *webui.Manager
	eventCh chan webui.Event
}

func NewPanel(cfg *config.Manager, st *state.RuntimeState, coreMgr *webui.Manager, eventCh chan webui.Event) *Panel {
	return &Panel{
		cfg:     cfg,
		st:      st,
		coreMgr: coreMgr,
		eventCh: eventCh,
	}
}

func (p *Panel) Open(forceSync func()) error {
	if p.st.GetPhase() != domain.PhaseRunning {
		return errors.New("内核尚未就绪，请稍候")
	}

	cfg := p.cfg.GetConfig()
	addr := p.cfg.GetEffectiveExternalController()
	secret := p.cfg.GetEffectiveSecret(cfg.Config.Secret)
	online := p.cfg.IsRemoteWebUIEnabled()
	sysBrowser := p.cfg.IsSystemBrowserEnabled()
	remoteURL := p.cfg.GetEffectiveRemoteWebUIURL()

	slog.Info("启动 Web 面板", "addr", addr, "sys_browser", sysBrowser, "remote", online)

	wcfg := webui.Config{
		APIAddr:            addr,
		Secret:             secret,
		ProxyPort:          p.cfg.GetEffectiveLocalProxyPortStr(),
		BaseDir:            p.cfg.BaseDir(),
		UIName:             cfg.Config.ExternalUIName,
		ForceSystemBrowser: sysBrowser,
		RemoteWebUI:        online,
		RemoteWebUIURL:     remoteURL,
	}

	if forceSync != nil {
		forceSync()
	}
	go p.coreMgr.Launch(wcfg, p.eventCh)
	return nil
}

func (p *Panel) ReopenIfActive(forceSync func()) {
	if !p.coreMgr.IsActive() {
		return
	}
	p.coreMgr.Cleanup()

	go func() {
		for i := 0; i < 60; i++ {
			if p.st.IsExiting() {
				return
			}
			if p.st.GetPhase() == domain.PhaseRunning {
				_ = p.Open(forceSync)
				return
			}
			time.Sleep(200 * time.Millisecond)
		}
		slog.Warn("等待内核就绪超时，未恢复 Web 面板")
	}()
}

func (p *Panel) CopyPassword() error {
	cfg := p.cfg.GetConfig()
	secret := p.cfg.GetEffectiveSecret(cfg.Config.Secret)
	if secret == "" {
		return errors.New("Web 面板未设置访问密码")
	}

	if err := sys.WriteToClipboard(secret); err != nil {
		return fmt.Errorf("写入剪贴板失败: %w", err)
	}
	return nil
}

func (p *Panel) ClearCache() error {
	if p.coreMgr.IsActive() {
		return errors.New("Web 面板当前正在运行中，请先关闭面板")
	}

	cacheDir := filepath.Join(p.cfg.BaseDir(), domain.WebCacheDir)
	if err := os.RemoveAll(cacheDir); err != nil {
		return fmt.Errorf("清理缓存目录失败: %w", err)
	}
	return nil
}
