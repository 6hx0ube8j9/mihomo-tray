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

// apiSoftRestartCommand sends POST /restart to trigger native kernel process restart.
// Kept for fast in-place kernel restart without daemon intervention.
func (a *Application) apiSoftRestartCommand(ctx context.Context) error {
	cmdCtx, cmdCancel := context.WithTimeout(ctx, 2*time.Second)
	defer cmdCancel()

	if err := a.API.RestartKernel(cmdCtx); err != nil {
		return fmt.Errorf("向内核发送重启指令失败: %w", err)
	}

	waitCtx, waitCancel := context.WithTimeout(ctx, 3*time.Second)
	defer waitCancel()

	if err := a.API.WaitForReady(waitCtx); err != nil {
		return fmt.Errorf("内核重启就绪超时: %w", err)
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
		slog.Debug("尝试通过 API 执行热重载", "action", actionDesc)
		if err := a.apiHotReloadCommand(ctx, deployRes.RuntimeAbs); err == nil {
			slog.Info("配置热重载成功，内核配置已平滑更新")
			return nil
		}
		slog.Warn("API 热重载失败，退化为物理进程重启")
		a.Kernel.WriteCoreLog("KERNEL_TRANSITION", fmt.Sprintf("%s 热重载失败，执行冷启动", actionDesc))
	}

	a.executePhysicalRestart(a.Cfg.GetConfig())
	return nil
}

func (a *Application) ReloadConfig(ctx context.Context) error {
	if !a.State.TryBeginAction(state.ActionReload) {
		return fmt.Errorf("系统正在处理其他核心操作，请稍后重试")
	}
	defer func() {
		a.State.EndAction()
		a.ForcePushUIState()
	}()

	slog.Info("开始执行手动热重载")
	if err := a.Cfg.ReloadFromDisk(); err != nil {
		return fmt.Errorf("读取主配置文件失败: %w", err)
	}

	a.CheckAndReconcilePrivileges(false)

	if err := a.applyActiveConfig(ctx, "手动热重载"); err != nil {
		return err
	}

	a.restartWebUIIfOpen()
	return nil
}

func (a *Application) RestartKernel(ctx context.Context) error {
	if !a.State.TryBeginAction(state.ActionRestart) {
		return fmt.Errorf("系统正在重启或处理其他任务，请稍后重试")
	}
	defer func() {
		a.State.EndAction()
		a.ForcePushUIState()
	}()

	slog.Info("开始执行内核重启")
	a.CheckAndReconcilePrivileges(false)
	target := a.Cfg.GetActivePath()

	if target != "" {
		if err := a.Cfg.ValidatePhysicalFile(target); err != nil {
			return fmt.Errorf("目标配置文件校验失败: %w", err)
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
		a.Kernel.WriteCoreLog("CONFIG", fmt.Sprintf("运行配置落盘失败 [%s]:\n%v", targetRelPath, err))
		return nil, fmt.Errorf("装配运行配置失败: %w", err)
	}

	if deployRes.IsUnchanged && a.runtimeValidated {
		slog.Debug("运行配置未发生变动，跳过沙盒校验")
		a.State.SetActualTunDevice(deployRes.TunDevice)
		return deployRes, nil
	}

	kernelPath := core.GetKernelPath(a.Cfg.BaseDir())
	if err := core.ValidateConfig(kernelPath, a.Cfg.BaseDir(), deployRes.RuntimeAbs); err != nil {
		a.runtimeValidated = false
		a.Kernel.WriteCoreLog("CONFIG", fmt.Sprintf("内核校验配置文件失败 [%s]:\n%v", filepath.Base(targetRelPath), err))
		return nil, fmt.Errorf("内核不支持当前配置格式: %w", err)
	}

	a.runtimeValidated = true
	a.State.SetActualTunDevice(deployRes.TunDevice)
	return deployRes, nil
}

func (a *Application) SyncRuntimeConfig() {
	activePath := a.Cfg.GetActivePath()

	if activePath != "" {
		if err := a.Cfg.ValidatePhysicalFile(activePath); err != nil {
			slog.Warn("活跃配置文件无效，已取消选中状态", "path", activePath, "err", err)
			a.Cfg.SetActiveProfile("")
			activePath = ""
		}
	}

	if _, err := a.prepareAndValidateConfig(activePath); err != nil {
		slog.Error("装配运行配置失败，系统保持空配置运行", "err", err)
		a.Cfg.SetActiveProfile("")
	}
}

func (a *Application) restartWebUIIfOpen() {
	wasOpen := a.WebUI.IsActive()
	a.WebUI.Cleanup()

	if wasOpen {
		slog.Debug("等待内核重启就绪后自动恢复 Web 面板")
		go func() {
			for i := 0; i < 60; i++ {
				if a.State.IsExiting() {
					return
				}
				if a.State.GetPhase() == domain.PhaseRunning {
					slog.Debug("内核已就绪，恢复 Web 面板")
					a.UICommandCh <- domain.UICommand{Action: domain.ActionOpenWebUI}
					return
				}
				time.Sleep(200 * time.Millisecond)
			}
			slog.Warn("等待内核就绪超时，未恢复 Web 面板")
		}()
	}
}
