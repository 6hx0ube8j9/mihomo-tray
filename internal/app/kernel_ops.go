package app

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"mihomo-tray/internal/core"
	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/state"
)

type kernelStrategy func(ctx context.Context) error

func (a *Application) executeKernelTransition(ctx context.Context, apiStrategy kernelStrategy, actionName string) {
	a.syncSystemProxy()
	isKernelRunning := a.State.GetPhase() == domain.PhaseRunning && !a.Kernel.IsPaused()

	if isKernelRunning {
		slog.Debug(fmt.Sprintf("尝试通过 API %s", actionName))
		if err := apiStrategy(ctx); err == nil {
			slog.Info(fmt.Sprintf("API %s 成功，内核状态已平滑同步", actionName))
			return
		}

		slog.Warn(fmt.Sprintf("API %s 受阻，退化为物理冷启动", actionName))
		a.Kernel.WriteCoreLog("KERNEL_TRANSITION", fmt.Sprintf("%s 异常转冷启动", actionName))
	} else {
		slog.Debug(fmt.Sprintf("内核当前未运行，准备物理拉起以应用 %s", actionName))
	}

	a.executePhysicalRestart(a.Cfg.GetConfig())
}

func (a *Application) executePhysicalRestart(cfg domain.TrayConfig) {
	if cfg.Config.Tun.Enable {
		a.State.SetTunRequestedTime(time.Now())
	}
	a.State.SetPhase(domain.PhaseInitializing)
	a.Kernel.HaltDaemon()
	a.Kernel.WakeDaemon()
	a.State.UpdateWebUISnapshot(cfg.Config.ExternalController, a.Cfg.GetEffectiveSecret(cfg.Config.Secret), cfg.Config.ExternalUIName)
	a.ForceSyncAPI()
}

func (a *Application) apiHotReloadCommand(ctx context.Context, runtimeAbs string) error {
	reqCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	payload := map[string]interface{}{"path": filepath.ToSlash(runtimeAbs)}
	if err := a.API.ForceReloadKernel(reqCtx, payload); err != nil {
		return err
	}

	time.Sleep(200 * time.Millisecond)
	a.ForceSyncAPI()
	a.syncAllConfig(ctx)
	return nil
}

func (a *Application) apiSoftRestartCommand(ctx context.Context) error {
	cmdCtx, cmdCancel := context.WithTimeout(ctx, 2*time.Second)
	defer cmdCancel()

	if err := a.API.RestartKernel(cmdCtx); err != nil {
		return fmt.Errorf("向内核发送指令失败: %w", err)
	}

	a.ForceSyncAPI()

	time.Sleep(300 * time.Millisecond)

	waitCtx, waitCancel := context.WithTimeout(ctx, 3*time.Second)
	defer waitCancel()

	if err := a.API.WaitForReady(waitCtx); err != nil {
		return fmt.Errorf("内核进程就绪超时: %w", err)
	}

	time.Sleep(200 * time.Millisecond)
	a.syncAllConfig(ctx)
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

	a.executeKernelTransition(ctx, func(c context.Context) error {
		return a.apiHotReloadCommand(c, deployRes.RuntimeAbs)
	}, actionDesc)

	return nil
}

func (a *Application) ReloadConfig(ctx context.Context) error {
	if !a.State.TryBeginAction(state.ActionReload) {
		return fmt.Errorf("系统正处于其他操作或重载中，忽略本次请求")
	}
	defer func() {
		a.State.EndAction()
		a.ForcePushUIState()
	}()

	slog.Info("开始执行手动重载")
	if err := a.Cfg.ReloadFromDisk(); err != nil {
		return fmt.Errorf("应用基础配置文件解析失败。\n\n%w", err)
	}

	cfg := a.Cfg.GetConfig()
	a.State.UpdateWebUISnapshot(cfg.Config.ExternalController, a.Cfg.GetEffectiveSecret(cfg.Config.Secret), cfg.Config.ExternalUIName)

	a.CheckAndReconcilePrivileges(false)

	if err := a.applyActiveConfig(ctx, "手动热重载"); err != nil {
		return err
	}

	a.restartWebUIIfOpen()
	return nil
}

func (a *Application) RestartKernel(ctx context.Context) error {
	if !a.State.TryBeginAction(state.ActionRestart) {
		return fmt.Errorf("系统正处于其他事务或正在重启中，忽略本次重启请求")
	}
	defer func() {
		a.State.EndAction()
		a.ForcePushUIState()
	}()

	slog.Info("开始执行手动内核重启")
	if err := a.Cfg.ReloadFromDisk(); err != nil {
		return fmt.Errorf("应用基础配置文件存在格式错误。\n\n%w", err)
	}
	
	cfg := a.Cfg.GetConfig()
	a.State.UpdateWebUISnapshot(cfg.Config.ExternalController, a.Cfg.GetEffectiveSecret(cfg.Config.Secret), cfg.Config.ExternalUIName)
	a.ForcePushUIState()

	a.CheckAndReconcilePrivileges(false)
	target := a.Cfg.GetActivePath()

	if target != "" {
		if err := a.Cfg.ValidatePhysicalFile(target); err != nil {
			return fmt.Errorf("目标配置文件读取失败，请求已取消。\n\n%w", err)
		}
	}

	if _, err := a.prepareAndValidateConfig(target); err != nil {
		return err
	}

	a.executeKernelTransition(ctx, a.apiSoftRestartCommand, "API 软重启")

	a.restartWebUIIfOpen()
	return nil
}

func (a *Application) prepareAndValidateConfig(targetRelPath string) (*core.DeployResult, error) {
	cfg := a.Cfg.GetConfig()
	deployRes, err := core.DeployRuntimeConfig(cfg, targetRelPath, a.Cfg.BaseDir())
	if err != nil {
		a.Kernel.WriteCoreLog("CONFIG", fmt.Sprintf("运行配置落盘失败 [%s]:\n%v", targetRelPath, err))
		return nil, fmt.Errorf("运行配置文件装配失败。\n\n%w", err)
	}

	kernelPath := core.GetKernelPath(a.Cfg.BaseDir())
	if err := core.ValidateConfig(kernelPath, a.Cfg.BaseDir(), deployRes.RuntimeAbs); err != nil {
		a.Kernel.WriteCoreLog("CONFIG", fmt.Sprintf("配置内核兼容性校验失败 [%s]:\n%v", filepath.Base(targetRelPath), err))
		return nil, fmt.Errorf("内核不支持该配置文件，加载失败。\n\n%w", err)
	}

	a.State.SetActualTunDevice(deployRes.TunDevice)
	return deployRes, nil
}

func (a *Application) SyncRuntimeConfig() {
	activePath := a.Cfg.GetActivePath()

	if activePath != "" {
		if err := a.Cfg.ValidatePhysicalFile(activePath); err != nil {
			slog.Warn("本地活跃配置失效，已自动取消选中", "path", activePath, "err", err)
			a.Cfg.SetActiveProfile("")
			activePath = ""
		}
	}

	if _, err := a.prepareAndValidateConfig(activePath); err != nil {
		slog.Error("生成或校验运行配置失败，系统退入空转保护", "err", err)
		a.Cfg.SetActiveProfile("")
	}
}

func (a *Application) restartWebUIIfOpen() {
	wasOpen := a.WebUI.IsActive()
	a.WebUI.Cleanup()

	if wasOpen {
		slog.Debug("等待内核就绪以恢复 Web 面板")
		go func() {
			for i := 0; i < 50; i++ {
				if a.State.IsExiting() {
					return
				}
				if a.State.GetPhase() == domain.PhaseRunning {
					slog.Debug("内核已就绪，触发 Web 面板自动恢复")
					a.UICommandCh <- domain.UICommand{Action: domain.ActionOpenWebUI}
					return
				}
				time.Sleep(200 * time.Millisecond)
			}
			slog.Warn("等待内核就绪超时，面板恢复失败")
		}()
	}
}
