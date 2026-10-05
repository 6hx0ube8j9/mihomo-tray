package app

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"mihomo-tray/internal/core"
	"mihomo-tray/internal/domain"
)

func (a *Application) prepareAndValidateConfig(targetRelPath string) (*core.DeployResult, error) {
	deployRes, err := a.deployAndSyncState(targetRelPath)
	if err != nil {
		return nil, fmt.Errorf("运行配置文件装配失败: %w", err)
	}

	kernelPath := core.GetKernelPath(a.Cfg.BaseDir())
	if err := core.ValidateConfig(kernelPath, a.Cfg.BaseDir(), deployRes.RuntimeAbs); err != nil {
		return nil, fmt.Errorf("该配置不被当前内核支持，已拦截加载：\n\n%w", err)
	}

	return deployRes, nil
}

func (a *Application) deployAndSyncState(targetRelPath string) (*core.DeployResult, error) {
	cfg := a.Cfg.GetConfig()
	deployRes, err := core.DeployRuntimeConfig(cfg, targetRelPath, a.Cfg.BaseDir())
	if err != nil {
		a.Kernel.WriteCoreLog("CONFIG", fmt.Sprintf("运行配置落盘失败 [%s]:\n%v", targetRelPath, err))
		return nil, err
	}

	a.State.SetActualTunDevice(deployRes.TunDevice)
	return deployRes, nil
}

func (a *Application) applyConfigTransaction(ctx context.Context, targetRelPath string) error {
	deployRes, err := a.prepareAndValidateConfig(targetRelPath)
	if err != nil {
		return err
	}

	a.syncSystemProxy()
	isKernelRunning := a.State.GetPhase() == domain.PhaseRunning && !a.Kernel.IsPaused()

	if isKernelRunning {
		slog.Info("尝试通过 API 热加载内核配置")
		
		reloadErr := a.executeAPIHotReload(ctx, deployRes.RuntimeAbs)
		if reloadErr == nil {
			slog.Info("内核已热更新为新配置", "target", targetRelPath)
			return nil
		}
		
		slog.Warn("内核热加载受阻，退化为物理硬重启拉起", "err", reloadErr)
		a.Kernel.WriteCoreLog("RELOAD", fmt.Sprintf("热加载异常转冷启动 | 错误: %v", reloadErr))
	} else {
		slog.Info("准备唤醒内核并应用新配置")
	}

	a.executePhysicalRestart(a.Cfg.GetConfig())
	return nil
}

func (a *Application) comboKernelRestart(ctx context.Context, targetRelPath string) error {
	if _, err := a.prepareAndValidateConfig(targetRelPath); err != nil {
		return err
	}

	a.syncSystemProxy()
	isKernelRunning := a.State.GetPhase() == domain.PhaseRunning && !a.Kernel.IsPaused()

	if isKernelRunning {
		slog.Info("尝试通过 API 执行内核热重启")
		
		restartErr := a.executeAPISoftRestart(ctx)
		if restartErr == nil {
			slog.Info("内核 API 重启成功，已挂载新配置")
			return nil
		}
		
		slog.Warn("API 热重启受阻，退化为底层进程冷启动", "err", restartErr)
		a.Kernel.WriteCoreLog("RESTART", fmt.Sprintf("API 重启异常转冷启动 | 错误: %v", restartErr))
	} else {
		slog.Info("内核当前未运行，准备直接唤醒底层进程")
	}

	a.executePhysicalRestart(a.Cfg.GetConfig())
	return nil
}

func (a *Application) executePhysicalRestart(cfg domain.TrayConfig) {
	if cfg.Config.Tun.Enable {
		a.State.SetTunRequestedTime(time.Now())
	}
	a.State.SetPhase(domain.PhaseInitializing)
	a.Kernel.HaltDaemon()
	a.Kernel.WakeDaemon()
	a.State.UpdateWebUISnapshot(cfg.Config.ExternalController, a.Cfg.GetEffectiveSecret(cfg.Config.Secret), cfg.Config.ExternalUIName)
}

func (a *Application) executeAPIHotReload(ctx context.Context, runtimeAbs string) error {
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

func (a *Application) executeAPISoftRestart(ctx context.Context) error {
	cmdCtx, cmdCancel := context.WithTimeout(ctx, 2*time.Second)
	defer cmdCancel()

	if err := a.API.RestartKernel(cmdCtx); err != nil {
		return fmt.Errorf("发送指令失败: %w", err)
	}

	waitCtx, waitCancel := context.WithTimeout(ctx, 3*time.Second)
	defer waitCancel()

	if err := a.API.WaitForReady(waitCtx); err != nil {
		return fmt.Errorf("就绪超时: %w", err)
	}

	time.Sleep(200 * time.Millisecond)
	a.syncAllConfig(ctx)
	a.ForceSyncAPI()
	return nil
}

func (a *Application) ReloadConfig(ctx context.Context) error {
	if !a.State.TryBeginReload() {
		slog.Debug("系统正处于其他操作或重载中，忽略本次重载请求")
		return nil
	}

	slog.Info("开始重载配置")

	defer func() {
		a.State.SetReloading(false)
		a.ForcePushUIState()
	}()

	if err := a.Cfg.ReloadFromDisk(); err != nil {
		slog.Warn("读取基础配置失败", "err", err)
		return fmt.Errorf("应用基础配置文件存在格式错误，已取消重载。\n\n详情：\n%w", err)
	}

	a.CheckAndReconcilePrivileges(false)
	target := a.Cfg.GetActivePath()

	if target != "" {
		if err := a.Cfg.ValidatePhysicalFile(target); err != nil {
			return fmt.Errorf("目标配置异常，请求已取消。\n\n错误: %w", err)
		}
	}

	if err := a.applyConfigTransaction(ctx, target); err != nil {
		return err
	}

	a.restartWebUIIfOpen()
	return nil
}

func (a *Application) RestartKernel(ctx context.Context) error {
	if !a.State.TryBeginRestart() {
		slog.Debug("系统正处于其他事务或正在重启中，忽略本次重启请求")
		return nil
	}

	target := a.Cfg.GetActivePath()
	if target != "" {
		if err := a.Cfg.ValidatePhysicalFile(target); err != nil {
			a.State.SetRestarting(false)
			a.ForcePushUIState()
			return fmt.Errorf("目标配置读取异常，请求已取消。\n\n错误: %w", err)
		}
	}

	slog.Info("开始重启内核")

	defer func() {
		a.State.SetRestarting(false)
		a.ForcePushUIState()
	}()

	if err := a.Cfg.ReloadFromDisk(); err != nil {
		slog.Warn("重启前读取配置失败", "err", err)
		return fmt.Errorf("应用基础配置文件存在格式错误，已取消重启。\n\n详情：\n%w", err)
	}

	a.CheckAndReconcilePrivileges(false)

	if err := a.comboKernelRestart(ctx, target); err != nil {
		return err
	}

	a.restartWebUIIfOpen()
	return nil
}

func (a *Application) SyncRuntimeConfig() {
	activePath := a.Cfg.GetActivePath()

	if activePath != "" {
		if err := a.Cfg.ValidatePhysicalFile(activePath); err != nil {
			slog.Warn("本地配置文件失效，已取消选中状态", "path", activePath, "err", err)
			a.Cfg.SetActiveProfile("")
			activePath = ""
		}
	}

	if _, err := a.deployAndSyncState(activePath); err != nil {
		slog.Error("生成运行配置失败，应用将暂停代理", "err", err)
		a.Cfg.SetActiveProfile("")
	}
}

func (a *Application) restartWebUIIfOpen() {
	wasOpen := a.WebUI.IsActive()
	a.WebUI.Cleanup()

	if wasOpen {
		slog.Debug("等待内核就绪，尝试恢复 Web 面板")

		go func() {
			for i := 0; i < 50; i++ {
				if a.State.IsExiting() {
					return
				}

				if a.State.GetPhase() == domain.PhaseRunning {
					slog.Debug("内核已就绪，正在自动恢复 Web 面板")
					a.UICommandCh <- domain.UICommand{Action: domain.ActionOpenWebUI}
					return
				}
				time.Sleep(200 * time.Millisecond)
			}
			slog.Warn("等待内核就绪超时，恢复 Web 面板失败")
		}()
	}
}
