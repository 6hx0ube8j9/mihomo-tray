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
		return fmt.Errorf("内核服务尚未启动完成，请稍候重试")
	}

	cfg := a.Cfg.GetConfig()
	apiAddr, secret, uiName := a.State.GetWebUISnapshot()

	slog.Info("正在打开 Web 面板", "system_browser", *cfg.General.SystemBrowser, "remote_webui", *cfg.General.RemoteWebUI)

	wcfg := webui.Config{
		APIAddr:            apiAddr,
		Secret:             secret,
		ProxyPort:          a.Cfg.GetEffectiveMixedPortStr(),
		BaseDir:            a.Cfg.BaseDir(),
		UIName:             uiName,
		ForceSystemBrowser: *cfg.General.SystemBrowser,
		RemoteWebUI:        *cfg.General.RemoteWebUI,
		RemoteWebUIURL:     *cfg.General.RemoteWebUIURL,
	}

	go a.WebUI.Launch(wcfg, a.webuiEventCh)
	return nil
}

func (a *Application) CopyWebUIPassword() error {
	_, secret, _ := a.State.GetWebUISnapshot()
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
