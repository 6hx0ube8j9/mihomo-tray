package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"mihomo-tray/internal/core"
	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/state"
)

func (a *Application) executePhysicalRestart(cfg domain.TrayConfig) {
	slog.Debug("执行内核物理重启", "tun", cfg.Config.Tun.Enable)
	if cfg.Config.Tun.Enable {
		a.State.SetTunRequestedTime(time.Now())
	}
	a.State.SetPhase(domain.PhaseInitializing)
	a.Kernel.HaltDaemon()
	a.Kernel.WakeDaemon()
}

func (a *Application) apiHotReloadCommand(ctx context.Context, runtimeAbs string) error {
	reqCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	if err := a.API.ForceReloadKernel(reqCtx, runtimeAbs); err != nil {
		return err
	}

	time.Sleep(200 * time.Millisecond)
	a.syncAllConfig(ctx)
	a.ForceSyncAPI()
	return nil
}

func (a *Application) applyActiveConfig(ctx context.Context, actionDesc string) error {
	return a.DeployAndApplyConfig(ctx, a.Cfg.GetActivePath(), actionDesc)
}

func (a *Application) DeployAndApplyConfig(ctx context.Context, targetRelPath, actionDesc string) error {
	if targetRelPath != "" {
		if err := a.Cfg.ValidatePhysicalFile(targetRelPath); err != nil {
			return err
		}
	}

	cfg := a.Cfg.GetConfig()
	deployRes, err := core.DeployRuntimeConfig(cfg, targetRelPath, a.Cfg.BaseDir())
	if err != nil {
		profileName := filepath.Base(targetRelPath)
		if profileName == "." || profileName == "" {
			profileName = "空配置"
		}
		return fmt.Errorf("%s: %w", profileName, err)
	}

	a.State.SetActualTunDevice(deployRes.TunDevice)
	a.syncSystemProxy()

	isKernelRunning := a.State.GetPhase() == domain.PhaseRunning && !a.Kernel.IsPaused()
	if isKernelRunning {
		slog.Debug("执行配置重载", "action", actionDesc)
		if err := a.apiHotReloadCommand(ctx, deployRes.RuntimeAbs); err == nil {
			slog.Debug("配置热重载成功", "target", targetRelPath)
			return nil
		}
		slog.Warn("热重载配置失败，自动降级为物理重启内核")
	}

	a.executePhysicalRestart(cfg)
	return nil
}

func (a *Application) ReloadConfig(ctx context.Context) error {
	if !a.State.TryBeginAction(state.ActionReload) {
		return errors.New("系统繁忙，请稍后重试")
	}

	a.ForcePushUIState()

	defer func() {
		a.State.EndAction()
		a.ForcePushUIState()
	}()

	slog.Debug("用户指令，准备重载配置")

	if err := a.Cfg.ReloadFromDisk(); err != nil {
		return fmt.Errorf("读取配置失败: %w", err)
	}

	a.CheckAndReconcilePrivileges(false)

	if err := a.applyActiveConfig(ctx, "配置重载"); err != nil {
		return err
	}

	a.restartWebUIIfOpen()
	return nil
}

func (a *Application) RestartKernel(ctx context.Context) error {
	if !a.State.TryBeginAction(state.ActionRestart) {
		return errors.New("系统繁忙，请稍后重试")
	}

	a.ForcePushUIState()
	defer func() {
		a.State.EndAction()
		a.ForcePushUIState()
	}()

	slog.Debug("用户指令，准备物理重启内核")
	a.CheckAndReconcilePrivileges(false)

	a.SyncRuntimeConfig()
	cfg := a.Cfg.GetConfig()

	a.executePhysicalRestart(cfg)
	a.restartWebUIIfOpen()
	return nil
}

func (a *Application) SyncRuntimeConfig() {
	activePath := a.Cfg.GetActivePath()
	if activePath != "" {
		if err := a.Cfg.ValidatePhysicalFile(activePath); err != nil {
			a.FallbackToEmptyProfile(context.Background(), "启动校验文件失效")
			activePath = ""
		}
	}

	cfg := a.Cfg.GetConfig()
	deployRes, err := core.DeployRuntimeConfig(cfg, activePath, a.Cfg.BaseDir())
	if err != nil {
		slog.Error("底稿配置内容存在错误，回退为空配置", "err", err)
		a.FallbackToEmptyProfile(context.Background(), "底稿配置内容校验失败")
		return
	}
	a.State.SetActualTunDevice(deployRes.TunDevice)
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
			slog.Warn("等待内核就绪超时，未恢复 Web 面板")
		}()
	}
}
