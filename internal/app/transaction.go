package app

import (
	"context"
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
	"mihomo-tray/internal/webui"
)

func (a *Application) applyConfigTransaction(ctx context.Context, targetRelPath string) error {
	cfg := a.Cfg.GetConfig()
	
	_, extracted, err := core.BuildRuntimeYAML(cfg, targetRelPath, a.Cfg.BaseDir())
	if err != nil {
		return fmt.Errorf("生成运行时配置失败: %w", err)
	}

	runtimeAbs := filepath.Join(a.Cfg.BaseDir(), domain.RuntimeConfigName)
	isKernelRunning := a.State.GetPhase() == domain.PhaseRunning

	if isKernelRunning {
		reqCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()

		payload := map[string]interface{}{"path": filepath.ToSlash(runtimeAbs)}
		_, err := a.API.DoRequest(reqCtx, "PUT", "/configs?force=true", payload)

		if err != nil {
			logMsg := fmt.Errorf("热重载被内核拒绝 | 配置: %s | 原因: %v", targetRelPath, err)
			a.Kernel.WriteCoreLog("ERROR", logMsg.Error())
			slog.Error("内核拒载，事务回滚", "target", targetRelPath)
			return err
		}
		slog.Info("内核热载成功")
	} else {
		slog.Info("使用新配置唤醒内核")
	}

	a.Cfg.SetActiveProfile(targetRelPath)
	if tunDev, ok := extracted["tun_device"]; ok {
		a.State.SetActualTunDevice(tunDev)
	}

	a.syncSystemProxy()

	if !isKernelRunning {
		a.Kernel.WakeDaemon()
		a.State.UpdateWebUISnapshot(cfg.Config.ExternalController, *cfg.Config.Secret, cfg.Config.ExternalUIName)
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
			slog.Warn("订阅更新中，拦截重复请求", "path", targetRelPath)
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
			ui.ShowErrorMessage(nil, "订阅更新拦截", err.Error())
		}
		slog.Error("订阅更新失败", "path", targetRelPath, "err", err)

		if isNew {
			slog.Info("新订阅拉取失败，清理回滚", "path", targetRelPath)
			absPath := filepath.Join(a.Cfg.BaseDir(), filepath.FromSlash(targetRelPath))
			_ = os.Remove(absPath)
			a.Cfg.RemoveProfile(targetRelPath)
			a.ForcePushUIState()
		}
		return
	}

	if success {
		slog.Info("订阅更新已完成", "path", targetRelPath)
		if a.Cfg.GetActivePath() == targetRelPath {
			slog.Info("活跃配置变更，触发热重载")
			_ = a.applyConfigTransaction(context.Background(), targetRelPath)
		}
		a.pushUIState()
	}
}

func (a *Application) ReloadConfig(ctx context.Context) {
	if a.State.IsReloading() {
		return
	}
	slog.Info("执行配置重载")
	a.State.SetReloading(true)

	go func() {
		defer a.State.SetReloading(false)
		defer a.pushUIState()

		if err := a.Cfg.ReloadFromDisk(); err != nil {
			slog.Warn("重载本地 JSON 配置失败", "err", err)
			ui.ShowErrorMessage(nil, "JSON 错误", "mihomo-tray.json 存在语法错误，已阻止热重载。\n\n详情：\n"+err.Error())
			return
		}

		a.checkAndReconcilePrivileges()
		target := a.Cfg.GetActivePath()

		if err := a.safePreflightCheck(target, "重载配置"); err != nil {
			return
		}

		if err := a.applyConfigTransaction(ctx, target); err != nil {
			ui.ShowErrorMessage(nil, "配置重载失败", "内核拒绝加载当前配置文件，请检查：\n\n"+err.Error())
		} else {
			a.restartWebUIIfOpen()
		}
	}()
}

func (a *Application) RestartKernel() {
	slog.Info("正在重启内核进程")
	a.State.SetRestarting(true)
	a.State.SetReloading(false)
	a.State.SetPhase(domain.PhaseInitializing)
	
	a.Kernel.HaltDaemon()
	
	if err := a.Cfg.ReloadFromDisk(); err != nil {
		slog.Warn("重启前重载本地 JSON 失败", "err", err)
		ui.ShowErrorMessage(nil, "JSON 错误", "mihomo-tray.json 存在语法错误，已阻止修改。\n\n详情：\n"+err.Error())
	} else {
		a.checkAndReconcilePrivileges()
	}
	
	a.SyncRuntimeConfig()

	cfg := a.Cfg.GetConfig()
	if cfg.Config.Tun.Enable {
		a.State.SetTunRequestedTime(time.Now())
	}

	a.Kernel.WakeDaemon()

	a.State.UpdateWebUISnapshot(cfg.Config.ExternalController, *cfg.Config.Secret, cfg.Config.ExternalUIName)
	
	a.pushUIState()

	a.restartWebUIIfOpen()
}

func (a *Application) SyncRuntimeConfig() {
	activePath := a.Cfg.GetActivePath()

	if activePath != "" {
		if err := a.Cfg.ValidatePhysicalFile(activePath); err != nil {
			slog.Warn("底稿校验失败，剥离失效配置", "path", activePath, "err", err)
			a.Cfg.SetActiveProfile("")
			activePath = ""
		}
	}

	cfg := a.Cfg.GetConfig()

	if _, extracted, err := core.BuildRuntimeYAML(cfg, activePath, a.Cfg.BaseDir()); err != nil {
		slog.Error("同步运行配置失败，系统将进入空转", "err", err)        
		a.Cfg.SetActiveProfile("")
	} else {
		if tunDev, ok := extracted["tun_device"]; ok {            
			a.State.SetActualTunDevice(tunDev)
		}
	}    
}

func (a *Application) restartWebUIIfOpen() {
	wasOpen := webui.IsActive()
	webui.Cleanup()

	if wasOpen {
		slog.Debug("等待内核就绪，尝试恢复 Web 面板")

		go func() {
			for i := 0; i < 50; i++ {
				if a.State.IsExiting() {
					return
				}

				if a.State.GetPhase() == domain.PhaseRunning {
					slog.Debug("内核已就绪，正在自动重启 Web 面板")
					a.UICommandCh <- domain.UICommand{Action: domain.ActionOpenWebUI}
					return
				}
				time.Sleep(200 * time.Millisecond)
			}
			slog.Warn("等待内核就绪超时，自动重启 Web 面板失败")
		}()
	}
}


func (a *Application) checkAndReconcilePrivileges() {
	cfg := a.Cfg.GetConfig()
	
	needsAdmin := false
	if cfg.General.RunAsAdmin { needsAdmin = true }
	if cfg.Config.Tun.Enable { needsAdmin = true }
	if cfg.General.Autostart != nil && *cfg.General.Autostart { needsAdmin = true }

	if needsAdmin && !sys.IsAdmin() {
		slog.Info("检测到 JSON 手动修改了越权配置，发起 UAC 提权")
		
		if err := sys.RunAsAdmin(a.Cfg.ExePath(), a.Cfg.BaseDir(), "--restarting"); err == nil {
			a.SafeShutdown(nil)
			os.Exit(0)
		}

		slog.Warn("UAC 提权未获授权，静默回滚越权状态")
		a.Cfg.Update(func(c *domain.TrayConfig) {
			c.General.RunAsAdmin = false
			c.Config.Tun.Enable = false
			b := false
			c.General.Autostart = &b
		})
	}
}
