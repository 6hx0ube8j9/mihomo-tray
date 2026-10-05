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
		return fmt.Errorf("内核尚未完全就绪，请稍后重试")
	}

	cfg := a.Cfg.GetConfig()
	apiAddr, secret, uiName := a.State.GetWebUISnapshot()

	slog.Info("准备唤起 Web 面板", "强制系统浏览器", *cfg.General.SystemBrowser, "使用在线面板", *cfg.General.RemoteWebUI)

	wcfg := webui.Config{
		APIAddr:            apiAddr,
		Secret:             secret,
		ProxyPort:          strconv.Itoa(a.Cfg.GetEffectivePort(cfg.Config.MixedPort, domain.DefaultMixedPort)),
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
		return fmt.Errorf("当前面板允许无密码访问，无需复制")
	}
	
	if err := sys.WriteToClipboard(secret); err != nil {
		return fmt.Errorf("系统剪贴板写入受阻，请检查系统设置")
	}
	return nil
}

func (a *Application) ClearWebUICache() error {
	cacheDir := filepath.Join(a.Cfg.BaseDir(), "webcache")
	if err := os.RemoveAll(cacheDir); err != nil {
		return fmt.Errorf("本地缓存文件可能正被系统或其他程序占用。\n\n%w", err)
	}
	return nil
}
