package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"mihomo-tray/internal/core"
	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/sys"
	"mihomo-tray/internal/ui"
)

func (a *Application) applyConfigTransaction(ctx context.Context, targetRelPath string) error {
	cfg := a.Cfg.GetConfig()
	
	_, extracted, err := core.BuildRuntimeYAML(cfg, targetRelPath, a.Cfg.BaseDir())
	if err != nil {
		return fmt.Errorf("生成运行配置失败: %w", err)
	}

	runtimeAbs := filepath.Join(a.Cfg.BaseDir(), domain.RuntimeConfigName)
	isKernelRunning := a.State.GetPhase() == domain.PhaseRunning

	if isKernelRunning {
		reqCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()

		payload := map[string]interface{}{"path": filepath.ToSlash(runtimeAbs)}
		err := a.API.ForceReloadKernel(reqCtx, payload)

		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) || os.IsTimeout(err) {
				slog.Error("加载配置超时", "target", targetRelPath)
				a.Kernel.WriteCoreLog("ERROR", fmt.Sprintf("加载配置超时 | 配置: %s", targetRelPath))
			} else {
				slog.Error("加载配置失败", "target", targetRelPath, "err", err)
				a.Kernel.WriteCoreLog("ERROR", fmt.Sprintf("加载配置失败 | 配置: %s | 原因: %v", targetRelPath, err))
				return err
			}
		} else {
			slog.Info("新配置已生效")
		}
	} else {
		slog.Info("准备唤醒内核并应用新配置")
	}

	a.Cfg.SetActiveProfile(targetRelPath)
	if tunDev, ok := extracted["tun_device"]; ok {
		a.State.SetActualTunDevice(tunDev)
	}

	a.syncSystemProxy()

	if !isKernelRunning {
		a.Kernel.WakeDaemon()
		a.State.UpdateWebUISnapshot(cfg.Config.ExternalController, a.Cfg.GetEffectiveSecret(cfg.Config.Secret), cfg.Config.ExternalUIName)
	} else {
		time.Sleep(500 * time.Millisecond)
		a.syncAllConfig(ctx)
		select {
		case a.apiPollCh <- struct{}{}:
		default:
		}
	}

	return nil
}

func (a *Application) executeRemoteUpdate(ctx context.Context, targetRelPath string, isManual bool, isNew bool) {
	if !a.State.TryAcquireProfileLock(targetRelPath) {
		if isManual {
			slog.Warn("拦截重复更新请求", "path", targetRelPath)
		}
		return
	}
	defer a.State.ReleaseProfileLock(targetRelPath)

	validator := func(tmpPath string) error {
		exePath := core.GetKernelPath(a.Cfg.BaseDir())
		return core.ValidateConfig(exePath, a.Cfg.BaseDir(), tmpPath)
	}

	cfg := a.Cfg.GetConfig()
	port := strconv.Itoa(a.Cfg.GetEffectivePort(cfg.Config.MixedPort, domain.DefaultMixedPort))
	
	success, err := a.Cfg.UpgradeSubscription(ctx, targetRelPath, port, validator)

	if err != nil {
		if isManual {
			ui.ShowErrorMessage(nil, "更新配置失败", "无法完成订阅更新，请检查网络或链接状态：\n\n"+err.Error())
		}
		slog.Error("更新配置失败", "path", targetRelPath, "err", err)

		if isNew {
			slog.Info("清理无效订阅文件", "path", targetRelPath)
			absPath := filepath.Join(a.Cfg.BaseDir(), filepath.FromSlash(targetRelPath))
			_ = os.Remove(absPath)
			a.Cfg.RemoveProfile(targetRelPath)
			a.ForcePushUIState()
		}
		return
	}

	if success {
		slog.Info("更新配置成功", "path", targetRelPath)
		if a.Cfg.GetActivePath() == targetRelPath {
			slog.Info("当前活跃配置已更新，执行重载")
			_ = a.applyConfigTransaction(context.Background(), targetRelPath)
		}
		a.pushUIState()
	}
}

func (a *Application) ReloadConfig(ctx context.Context) {
	if a.State.IsReloading() {
		return
	}
	slog.Info("开始重载配置")
	a.State.SetReloading(true)

	go func() {
		defer func() {
			a.State.MarkMaintenanceEnd()
			a.State.SetReloading(false)
		}()
		defer a.pushUIState()

		if err := a.Cfg.ReloadFromDisk(); err != nil {
			slog.Warn("读取基础配置失败", "err", err)
			ui.ShowErrorMessage(nil, "读取配置失败", "应用基础配置文件存在格式错误，已取消重载。\n\n详情：\n"+err.Error())
			return
		}

		a.checkAndReconcilePrivileges(false)
		target := a.Cfg.GetActivePath()

		if err := a.safePreflightCheck(target, "重载配置"); err != nil {
			return
		}

		if err := a.applyConfigTransaction(ctx, target); err != nil {
			ui.ShowErrorMessage(nil, "应用配置失败", "内核拒绝加载当前配置文件，请检查语法或依赖：\n\n"+err.Error())
		} else {
			a.restartWebUIIfOpen()
		}
	}()
}

func (a *Application) RestartKernel() {
	slog.Info("开始重启内核")
	a.State.SetRestarting(true)
	a.State.SetReloading(false)

	defer func() {
		a.State.MarkMaintenanceEnd()
		a.State.SetRestarting(false)
	}()

	if err := a.Cfg.ReloadFromDisk(); err != nil {
		slog.Warn("重启前读取配置失败", "err", err)
		ui.ShowErrorMessage(nil, "读取配置失败", "应用基础配置文件存在格式错误，已取消重启。\n\n详情：\n"+err.Error())
		return
	} else {
		a.checkAndReconcilePrivileges(false)
	}

	a.SyncRuntimeConfig()

	cfg := a.Cfg.GetConfig()
	if cfg.Config.Tun.Enable {
		a.State.SetTunRequestedTime(time.Now())
	}

	if a.State.GetPhase() == domain.PhaseRunning && a.restartKernelViaAPI() {
		a.State.SetPhase(domain.PhaseRunning)
	} else {
		a.State.SetPhase(domain.PhaseInitializing)
		a.Kernel.HaltDaemon()
		a.Kernel.WakeDaemon()
	}

	a.State.UpdateWebUISnapshot(cfg.Config.ExternalController, a.Cfg.GetEffectiveSecret(cfg.Config.Secret), cfg.Config.ExternalUIName)
	a.pushUIState()
	a.restartWebUIIfOpen()
}

func (a *Application) restartKernelViaAPI() bool {
	cmdCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	
	if err := a.API.RestartKernel(cmdCtx); err != nil {
		slog.Warn("API 重启请求失败", "err", err)
		return false
	}

	waitCtx, waitCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer waitCancel()
	
	if err := a.API.WaitForReady(waitCtx); err != nil {
		slog.Warn("等待内核就绪超时", "err", err)
		return false
	}

	slog.Info("内核已通过 API 重启就绪")
	
	syncCtx, syncCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer syncCancel()
	
	a.syncAllConfig(syncCtx)
	
	select {
	case a.apiPollCh <- struct{}{}:
	default:
	}
	
	return true
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

	cfg := a.Cfg.GetConfig()

	if _, extracted, err := core.BuildRuntimeYAML(cfg, activePath, a.Cfg.BaseDir()); err != nil {
		slog.Error("生成运行配置失败，应用将暂停代理", "err", err)        
		a.Cfg.SetActiveProfile("")
	} else {
		if tunDev, ok := extracted["tun_device"]; ok {            
			a.State.SetActualTunDevice(tunDev)
		}
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

func (a *Application) checkAndReconcilePrivileges(isStartup bool) {
	cfg := a.Cfg.GetConfig()
	
	needsAdmin := false
	if cfg.General.RunAsAdmin { needsAdmin = true }
	if cfg.Config.Tun.Enable { needsAdmin = true }
	if cfg.General.Autostart != nil && *cfg.General.Autostart { needsAdmin = true }

	if needsAdmin && !sys.IsAdmin() {
		if isStartup {
			slog.Warn("以普通权限启动，暂时停用提权功能 (TUN/始终管理员/开机自启)")
			a.revertPrivilegedConfig()
			return
		}

		slog.Info("配置需要管理员权限，正在尝试提权")
		
		if err := sys.RunAsAdmin(a.Cfg.ExePath(), a.Cfg.BaseDir(), "--restarting"); err == nil {
			slog.Info("提权成功，旧实例准备退出")
			if ui.GlobalEngine != nil {
				ui.GlobalEngine.Exit() 
			}
			return
		}

		slog.Warn("提权被取消或失败，已恢复普通权限配置")
		a.revertPrivilegedConfig()
	}
}

func (a *Application) revertPrivilegedConfig() {
	a.Cfg.Update(func(c *domain.TrayConfig) {
		c.General.RunAsAdmin = false
		c.Config.Tun.Enable = false
		b := false
		c.General.Autostart = &b
	})
}
