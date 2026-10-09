package app

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/sys"
	"mihomo-tray/internal/webui"
)

func (a *Application) OpenWebUI() error {
	if a.State.GetPhase() != domain.PhaseRunning {
		return errors.New("内核尚未就绪")
	}

	addr, secret, online, sysBrowser, remoteURL := a.GetControllerConfigSnapshot()
	cfg := a.Cfg.GetConfig()

	slog.Info("启动 Web 面板", "addr", addr, "sys_browser", sysBrowser, "remote", online)

	wcfg := webui.Config{
		APIAddr:            addr,
		Secret:             secret,
		ProxyPort:          a.Cfg.GetEffectiveMixedPortStr(),
		BaseDir:            a.Cfg.BaseDir(),
		UIName:             cfg.Config.ExternalUIName,
		ForceSystemBrowser: sysBrowser,
		RemoteWebUI:        online,
		RemoteWebUIURL:     remoteURL,
	}

	a.ForceSyncAPI()
	go a.WebUI.Launch(wcfg, a.webuiEventCh)
	return nil
}

func (a *Application) CopyWebUIPassword() error {
	_, secret, _, _, _ := a.GetControllerConfigSnapshot()
	if secret == "" {
		return errors.New("未设置访问密码")
	}

	if err := sys.WriteToClipboard(secret); err != nil {
		return fmt.Errorf("写入剪贴板失败: %w", err)
	}
	return nil
}

func (a *Application) ClearWebUICache() error {
	if a.WebUI.IsActive() {
		return errors.New("Web 面板运行中，请先关闭面板")
	}

	cacheDir := filepath.Join(a.Cfg.BaseDir(), domain.WebCacheDir)
	if err := os.RemoveAll(cacheDir); err != nil {
		return fmt.Errorf("清理缓存失败: %w", err)
	}
	return nil
}
