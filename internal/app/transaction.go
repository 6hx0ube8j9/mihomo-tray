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

func (a *Application) executePhysicalRestart(cfg domain.TrayConfig) {
	if cfg.Config.Tun.Enable {
		a.State.SetTunRequestedTime(time.Now())
	}
	a.State.SetPhase(domain.PhaseInitializing)
	a.Kernel.HaltDaemon()
	a.Kernel.WakeDaemon()
	a.State.UpdateWebUISnapshot(cfg.Config.ExternalController, a.Cfg.GetEffectiveSecret(cfg.Config.Secret), cfg.Config.ExternalUIName)
}

func (a *Application) applyConfigTransaction(ctx context.Context, targetRelPath string) error {
	deployRes, err := a.deployAndSyncState(targetRelPath)
	if err != nil {
		return fmt.Errorf("交付运行配置失败: %w", err)
	}

	a.syncSystemProxy()
	isKernelRunning := a.State.GetPhase() == domain.PhaseRunning && !a.Kernel.IsPaused()

	if isKernelRunning {
		reqCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()

		payload := map[string]interface{}{"path": filepath.ToSlash(deployRes.RuntimeAbs)}
		err := a.API.ForceReloadKernel(reqCtx, payload)

		if err == nil {
			slog.Info("内核已热更新为新配置", "target", targetRelPath)
			time.Sleep(200 * time.Millisecond)
			a.syncAllConfig(ctx)
			a.ForceSyncAPI()
			return nil
		}

		slog.Warn("内核热加载受阻，退化为物理硬重启拉起", "err", err)
		a.Kernel.WriteCoreLog("RELOAD", fmt.Sprintf("热加载异常转冷启动 | 错误: %v", err))
	} else {
		slog.Info("准备唤醒内核并应用新配置")
	}

	cfg := a.Cfg.GetConfig()
	a.executePhysicalRestart(cfg)
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
		return fmt.Errorf("运行配置文件装配失败：\n\n%w", err)
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

	slog.Info("开始物理硬重启内核")

	defer func() {
		a.State.SetRestarting(false)
		a.ForcePushUIState()
	}()

	if err := a.Cfg.ReloadFromDisk(); err != nil {
		slog.Warn("重启前读取配置失败", "err", err)
		return fmt.Errorf("应用基础配置文件存在格式错误，已取消重启。\n\n详情：\n%w", err)
	}

	a.CheckAndReconcilePrivileges(false)

	if _, err := a.deployAndSyncState(target); err != nil {
		return fmt.Errorf("运行配置文件装配失败，内核拒绝重启：\n\n%w", err)
	}

	cfg := a.Cfg.GetConfig()
	a.executePhysicalRestart(cfg)
	a.restartWebUIIfOpen()

	return nil
}

func (a *Application) RestartKernelViaAPI(ctx context.Context) error {
	if a.State.GetPhase() != domain.PhaseRunning {
		return fmt.Errorf("内核当前未处于运行状态，无法执行 API 重启")
	}

	if !a.State.TryBeginRestart() {
		return fmt.Errorf("系统正处于其他事务中，无法启动重启")
	}

	slog.Info("开始请求内核 API 重启")

	defer func() {
		a.State.SetRestarting(false)
		a.ForcePushUIState()
	}()

	cmdCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	if err := a.API.RestartKernel(cmdCtx); err != nil {
		a.Kernel.WriteCoreLog("RELOAD", fmt.Sprintf("内核 API 重启指令发送失败: %v", err))
		return fmt.Errorf("发送 API 重启指令失败: %w", err)
	}

	waitCtx, waitCancel := context.WithTimeout(ctx, 3*time.Second)
	defer waitCancel()

	if err := a.API.WaitForReady(waitCtx); err != nil {
		a.Kernel.WriteCoreLog("RELOAD", fmt.Sprintf("内核 API 重启后就绪超时: %v", err))
		return fmt.Errorf("等待内核重启就绪超时: %w", err)
	}

	slog.Info("内核已通过 API 重启就绪")

	syncCtx, syncCancel := context.WithTimeout(ctx, 3*time.Second)
	defer syncCancel()

	a.syncAllConfig(syncCtx)
	a.ForceSyncAPI()

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
