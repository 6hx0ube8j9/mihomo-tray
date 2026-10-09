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

	payload := map[string]interface{}{"path": filepath.ToSlash(runtimeAbs)}
	if err := a.API.ForceReloadKernel(reqCtx, payload); err != nil {
		return err
	}

	time.Sleep(200 * time.Millisecond)
	a.syncAllConfig(ctx)
	a.ForceSyncAPI()
	return nil
}

func (a *Application) DeployAndApplyConfig(ctx context.Context, targetRelPath, actionDesc string) error {
	cfg := a.Cfg.GetConfig()
	deployRes, err := core.DeployRuntimeConfig(cfg, targetRelPath, a.Cfg.BaseDir())
	if err != nil {
		a.Kernel.WriteCoreLog(domain.LogTagConfig, fmt.Sprintf("%s 失败 [%s]: %v", actionDesc, filepath.Base(targetRelPath), err))
		return errors.New("配置格式错误")
	}

	a.State.SetActualTunDevice(deployRes.TunDevice)
	a.syncSystemProxy()

	isKernelRunning := a.State.GetPhase() == domain.PhaseRunning && !a.Kernel.IsPaused()
	if isKernelRunning {
		slog.Debug("执行 API 热重载", "action", actionDesc)
		if err := a.apiHotReloadCommand(ctx, deployRes.RuntimeAbs); err == nil {
			slog.Info("配置热重载成功")
			return nil
		}
		slog.Warn("API 热重载失败，退化为物理重启")
		a.Kernel.WriteCoreLog(domain.LogTagKernelTransition, fmt.Sprintf("%s: 热重载失败，切换冷启动", actionDesc))
	}

	a.executePhysicalRestart(cfg)
	return nil
}

func (a *Application) ReloadConfig(ctx context.Context) error {
	if !a.State.TryBeginAction(state.ActionReload) {
		return errors.New("系统繁忙，请稍后重试")
	}
	defer func() {
		a.State.EndAction()
		a.ForcePushUIState()
	}()

	slog.Info("开始重载配置")
	if err := a.Cfg.ReloadFromDisk(); err != nil {
		return fmt.Errorf("读取配置失败: %w", err)
	}

	a.CheckAndReconcilePrivileges(false)

	target := a.Cfg.GetActivePath()
	if err := a.DeployAndApplyConfig(ctx, target, "配置重载"); err != nil {
		return err
	}

	a.restartWebUIIfOpen()
	return nil
}

func (a *Application) RestartKernel(ctx context.Context) error {
	if !a.State.TryBeginAction(state.ActionRestart) {
		return errors.New("系统繁忙，请稍后重试")
	}
	defer func() {
		a.State.EndAction()
		a.ForcePushUIState()
	}()

	slog.Info("开始重启内核")
	a.CheckAndReconcilePrivileges(false)

	target := a.Cfg.GetActivePath()
	if err := a.DeployAndApplyConfig(ctx, target, "重启内核"); err != nil {
		return err
	}

	a.restartWebUIIfOpen()
	return nil
}

func (a *Application) SyncRuntimeConfig() {
	activePath := a.Cfg.GetActivePath()
	if activePath != "" {
		if err := a.Cfg.ValidatePhysicalFile(activePath); err != nil {
			slog.Warn("活跃配置无效，取消选中", "path", activePath, "err", err)
			a.Cfg.SetActiveProfile("")
			activePath = ""
		}
	}

	cfg := a.Cfg.GetConfig()
	deployRes, err := core.DeployRuntimeConfig(cfg, activePath, a.Cfg.BaseDir())
	if err != nil {
		slog.Error("装配运行配置失败，切换空配置", "err", err)
		a.Cfg.SetActiveProfile("")
		_, _ = core.DeployRuntimeConfig(cfg, "", a.Cfg.BaseDir())
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
