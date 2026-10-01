package app

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"

	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/sys"
	"mihomo-tray/internal/webui"
)

func (a *Application) OpenWebUI() error {
	if a.State.GetPhase() != domain.PhaseRunning {
		return fmt.Errorf("内核未启动完成，暂无法打开 WebUI")
	}

	cfg := a.Cfg.GetConfig()
	apiAddr, secret, uiName := a.State.GetWebUISnapshot()

	slog.Info("正在打开面板", "强制系统浏览器", *cfg.General.SystemBrowser, "使用远程面板", *cfg.General.RemoteWebUI)

	wcfg := webui.Config{
		APIAddr:            apiAddr,
		Secret:             secret,
		ProxyPort:          strconv.Itoa(a.Cfg.GetEffectivePort(cfg.Config.MixedPort, domain.DefaultMixedPort)),
		BaseDir:            a.Cfg.BaseDir(),
		UIName:             uiName,
		ForceSystemBrowser: *cfg.General.SystemBrowser,
		RemoteWebUI:        *cfg.General.RemoteWebUI,
	}

	go a.WebUI.Launch(wcfg, a.webuiEventCh)
	return nil
}

func (a *Application) CopyWebUIPassword() error {
	_, secret, _ := a.State.GetWebUISnapshot()
	if secret == "" {
		return fmt.Errorf("当前 Web 面板无需密码即可访问")
	}
	
	if err := sys.WriteToClipboard(secret); err != nil {
		return fmt.Errorf("无法写入系统剪贴板。\n\n错误: %w", err)
	}
	return nil
}

func (a *Application) ClearWebUICache() error {
	cacheDir := filepath.Join(a.Cfg.BaseDir(), "webcache")
	if err := os.RemoveAll(cacheDir); err != nil {
		return fmt.Errorf("无法彻底清除缓存目录，文件可能正在被使用。\n\n错误: %w", err)
	}
	return nil
}
