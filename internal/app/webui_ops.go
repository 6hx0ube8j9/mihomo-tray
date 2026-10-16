package app

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/sys"
	"mihomo-tray/internal/webui"
)

func (a *Application) OpenWebUI() error {
	if a.State.GetPhase() != domain.PhaseRunning {
		return errors.New("内核尚未就绪，请稍候")
	}

	addr, secret, online, sysBrowser, remoteURL := a.Settings.GetControllerSnapshot()
	cfg := a.Cfg.GetConfig()

	slog.Info("启动 Web 面板", "addr", addr, "sys_browser", sysBrowser, "remote", online)

	wcfg := webui.Config{
		APIAddr:            addr,
		Secret:             secret,
		ProxyPort:          a.Cfg.GetEffectiveLocalProxyPortStr(),
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

func (a *Application) restartWebUIIfOpen() {
	wasOpen := a.WebUI.IsActive()
	a.WebUI.Cleanup()

	if wasOpen {
		go func() {
			for i := 0; i < 60; i++ {
				if a.State.IsExiting() {
					return
				}
				if a.State.GetPhase() == domain.PhaseRunning {
					a.UICommandCh <- domain.UICommand{Action: domain.ActionOpenWebUI}
					return
				}
				time.Sleep(200 * time.Millisecond)
			}
			slog.Warn("等待内核就绪超时，未能自动恢复 Web 面板")
		}()
	}
}

func (a *Application) CopyWebUIPassword() error {
	_, secret, _, _, _ := a.Settings.GetControllerSnapshot()
	if secret == "" {
		return errors.New("外部控制未设置访问密码")
	}

	if err := sys.WriteToClipboard(secret); err != nil {
		return fmt.Errorf("写入系统剪贴板失败: %w", err)
	}
	return nil
}

func (a *Application) ClearWebUICache() error {
	if a.WebUI.IsActive() {
		return errors.New("Web 面板当前正在运行中，请先关闭面板")
	}

	cacheDir := filepath.Join(a.Cfg.BaseDir(), domain.WebCacheDir)
	if err := os.RemoveAll(cacheDir); err != nil {
		return fmt.Errorf("清理缓存目录失败: %w", err)
	}
	return nil
}
