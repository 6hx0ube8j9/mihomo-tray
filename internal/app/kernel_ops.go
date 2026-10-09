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

func (a *Application) applyActiveConfig(ctx context.Context, actionDesc string) error {
	target := a.Cfg.GetActivePath()
	if target != "" {
		if err := a.Cfg.ValidatePhysicalFile(target); err != nil {
			return err
		}
	}

	deployRes, err := a.prepareAndValidateConfig(target)
	if err != nil {
		return err
	}

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

	a.executePhysicalRestart(a.Cfg.GetConfig())
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
	defer func() {
		a.State.EndAction()
		a.ForcePushUIState()
	}()

	slog.Info("开始重启内核")
	a.CheckAndReconcilePrivileges(false)
	target := a.Cfg.GetActivePath()

	if target != "" {
		if err := a.Cfg.ValidatePhysicalFile(target); err != nil {
			return fmt.Errorf("配置文件错误: %w", err)
		}
	}

	if _, err := a.prepareAndValidateConfig(target); err != nil {
		return err
	}

	a.syncSystemProxy()
	a.executePhysicalRestart(a.Cfg.GetConfig())

	a.restartWebUIIfOpen()
	return nil
}

func (a *Application) prepareAndValidateConfig(targetRelPath string) (*core.DeployResult, error) {
	cfg := a.Cfg.GetConfig()
	deployRes, err := core.DeployRuntimeConfig(cfg, targetRelPath, a.Cfg.BaseDir())
	if err != nil {
		a.Kernel.WriteCoreLog(domain.LogTagConfig, fmt.Sprintf("装配失败 [%s]: %v", filepath.Base(targetRelPath), err))
		return nil, fmt.Errorf("配置生成失败: %w", err)
	}

	if deployRes.IsUnchanged && a.runtimeValidated {
		slog.Debug("配置无变动，跳过校验")
		a.State.SetActualTunDevice(deployRes.TunDevice)
		return deployRes, nil
	}

	kernelPath := core.GetKernelPath(a.Cfg.BaseDir())
	if err := core.ValidateConfig(kernelPath, a.Cfg.BaseDir(), deployRes.RuntimeAbs); err != nil {
		a.runtimeValidated = false
		a.Kernel.WriteCoreLog(domain.LogTagConfig, fmt.Sprintf("校验失败 [%s]: %v", filepath.Base(targetRelPath), err))
		return nil, fmt.Errorf("配置格式错误: %w", err)
	}

	a.runtimeValidated = true
	a.State.SetActualTunDevice(deployRes.TunDevice)
	return deployRes, nil
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

	if _, err := a.prepareAndValidateConfig(activePath); err != nil {
		slog.Error("配置装配失败，切换空配置", "err", err)
		a.Cfg.SetActiveProfile("")
	}
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
