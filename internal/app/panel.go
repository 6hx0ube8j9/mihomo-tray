package app

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"mihomo-tray/internal/config"
	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/state"
	"mihomo-tray/internal/sys"
	"mihomo-tray/internal/webui"
)

type Panel struct {
	cfg      *config.Manager
	st       *state.RuntimeState
	settings *SettingsService // 或叫做 Tuner
	coreMgr  *webui.Manager
	eventCh  chan webui.Event
}

func NewPanel(cfg *config.Manager, st *state.RuntimeState, settings *SettingsService, coreMgr *webui.Manager, eventCh chan webui.Event) *Panel {
	return &Panel{
		cfg:      cfg,
		st:       st,
		settings: settings,
		coreMgr:  coreMgr,
		eventCh:  eventCh,
	}
}

func (p *Panel) Open(forceSync func()) error {
	if p.st.GetPhase() != domain.PhaseRunning {
		return errors.New("内核尚未就绪，请稍候")
	}

	addr, secret, online, sysBrowser, remoteURL := p.settings.GetControllerSnapshot()
	cfg := p.cfg.GetConfig()

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

	forceSync()
	go p.coreMgr.Launch(wcfg, p.eventCh)
	return nil
}

func (p *Panel) CopyPassword() error {
	_, secret, _, _, _ := p.settings.GetControllerSnapshot()
	if secret == "" {
		return errors.New("外部控制未设置访问密码")
	}

	if err := sys.WriteToClipboard(secret); err != nil {
		return fmt.Errorf("写入系统剪贴板失败: %w", err)
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
