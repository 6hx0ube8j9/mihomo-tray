package app

import (
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
		return fmt.Errorf("内核尚未就绪，请稍候重试")
	}

	addr, secret, online, sysBrowser, remoteURL := a.GetControllerConfigSnapshot()
	cfg := a.Cfg.GetConfig()

	slog.Info("正在打开 Web 面板", "addr", addr, "system_browser", sysBrowser, "remote_webui", online)

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
		return fmt.Errorf("当前面板未设置访问密码")
	}

	if err := sys.WriteToClipboard(secret); err != nil {
		return fmt.Errorf("写入系统剪贴板失败: %w", err)
	}
	return nil
}

func (a *Application) ClearWebUICache() error {
	cacheDir := filepath.Join(a.Cfg.BaseDir(), "webcache")
	if err := os.RemoveAll(cacheDir); err != nil {
		return fmt.Errorf("清理缓存目录失败，文件可能正被占用: %w", err)
	}
	return nil
}
