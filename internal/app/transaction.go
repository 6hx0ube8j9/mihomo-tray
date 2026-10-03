package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mihomo-tray/internal/core"
	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/fs"
)

func (a *Application) applyConfigTransaction(ctx context.Context, targetRelPath string) error {
	cfg := a.Cfg.GetConfig()
	sourceAbs := ""
	if targetRelPath != "" {
		sourceAbs = filepath.Join(a.Cfg.BaseDir(), filepath.FromSlash(targetRelPath))
	}
	
	mergedBytes, extracted, err := core.BuildRuntimeYAML(cfg, sourceAbs)
	if err != nil {
		return fmt.Errorf("底稿异常: %w", err)
	}

	if err := core.ValidateConfigContent(a.Cfg.ExePath(), a.Cfg.BaseDir(), mergedBytes); err != nil {
		return fmt.Errorf("最终生效配置存在语义错误，内核拒绝加载:\n\n%w", err)
	}

	runtimeAbs := filepath.Join(a.Cfg.BaseDir(), domain.RuntimeConfigName)
	existing, err := os.ReadFile(runtimeAbs)
	if err != nil || strings.TrimSpace(string(existing)) != strings.TrimSpace(string(mergedBytes)) {
		if err := fs.WriteAtomic(runtimeAbs, mergedBytes); err != nil {
			return fmt.Errorf("持久化正式配置失败: %w", err)
		}
	}

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
		a.ForceSyncAPI()
	}

	return nil
}

func (a *Application) ReloadConfig(ctx context.Context) error {
	if a.State.IsReloading() {
		return nil
	}
	slog.Info("开始重载配置")
	a.State.SetReloading(true)

	defer func() {
		a.State.SetReloading(false)
		a.pushUIState()
	}()

	if err := a.Cfg.ReloadFromDisk(); err != nil {
		slog.Warn("读取基础配置失败", "err", err)
		return fmt.Errorf("应用基础配置文件存在格式错误，已取消重载。\n\n详情：\n%w", err)
	}

	a.CheckAndReconcilePrivileges(false)
	target := a.Cfg.GetActivePath()

	if err := a.Cfg.ValidatePhysicalFile(target); err != nil {
		return fmt.Errorf("目标配置异常，请求已取消。\n\n错误: %w", err)
	}

	if err := a.applyConfigTransaction(ctx, target); err != nil {
		return fmt.Errorf("内核拒绝加载当前配置文件，请检查语法或依赖：\n\n%w", err)
	} 
	
	a.restartWebUIIfOpen()
	return nil
}

func (a *Application) RestartKernel(ctx context.Context) error {
	if a.State.IsRestarting() {
		return nil
	}
	
	target := a.Cfg.GetActivePath()
	if target != "" {
		if err := a.Cfg.ValidatePhysicalFile(target); err != nil {
			return fmt.Errorf("目标配置读取异常，请求已取消。\n\n错误: %w", err)
		}
		sourceAbs := filepath.Join(a.Cfg.BaseDir(), filepath.FromSlash(target))
		if err := a.verifyProfileSemantics(sourceAbs); err != nil {
			return fmt.Errorf("目标配置不合法，内核拒绝重启：\n\n%w", err)
		}
	}

	slog.Info("开始重启内核")
	a.State.SetRestarting(true)
	a.State.SetReloading(false)

	defer func() {
		a.State.SetRestarting(false)
		a.pushUIState()
	}()

	if err := a.Cfg.ReloadFromDisk(); err != nil {
		slog.Warn("重启前读取配置失败", "err", err)
		return fmt.Errorf("应用基础配置文件存在格式错误，已取消重启。\n\n详情：\n%w", err)
	}
	
	a.CheckAndReconcilePrivileges(false)
	a.SyncRuntimeConfig()

	cfg := a.Cfg.GetConfig()
	if cfg.Config.Tun.Enable {
		a.State.SetTunRequestedTime(time.Now())
	}

	if a.State.GetPhase() == domain.PhaseRunning && a.restartKernelViaAPI(ctx) {
		a.State.SetPhase(domain.PhaseRunning)
	} else {
		a.State.SetPhase(domain.PhaseInitializing)
		a.Kernel.HaltDaemon()
		a.Kernel.WakeDaemon()
	}

	a.State.UpdateWebUISnapshot(cfg.Config.ExternalController, a.Cfg.GetEffectiveSecret(cfg.Config.Secret), cfg.Config.ExternalUIName)
	
	a.restartWebUIIfOpen()
	
	return nil
}

func (a *Application) restartKernelViaAPI(ctx context.Context) bool {
	cmdCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	
	if err := a.API.RestartKernel(cmdCtx); err != nil {
		slog.Warn("API 重启请求失败", "err", err)
		return false
	}

	waitCtx, waitCancel := context.WithTimeout(ctx, 3*time.Second)
	defer waitCancel()
	
	if err := a.API.WaitForReady(waitCtx); err != nil {
		slog.Warn("等待内核就绪超时", "err", err)
		return false
	}

	slog.Info("内核已通过 API 重启就绪")
	
	syncCtx, syncCancel := context.WithTimeout(ctx, 3*time.Second)
	defer syncCancel()
	
	a.syncAllConfig(syncCtx)
	a.ForceSyncAPI()
	
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
	sourceAbs := ""
	if activePath != "" {
		sourceAbs = filepath.Join(a.Cfg.BaseDir(), filepath.FromSlash(activePath))
	}

	mergedBytes, extracted, err := core.BuildRuntimeYAML(cfg, sourceAbs)
	if err != nil {
		slog.Error("配置生成失败 (底稿语法异常)，应用将暂停代理", "err", err)        
		a.Cfg.SetActiveProfile("")
		return
	}

	if err := core.ValidateConfigContent(a.Cfg.ExePath(), a.Cfg.BaseDir(), mergedBytes); err != nil {
		slog.Error("终态配置语义错误，应用将暂停代理", "err", err)
		a.Cfg.SetActiveProfile("")
		return
	}

	runtimeAbs := filepath.Join(a.Cfg.BaseDir(), domain.RuntimeConfigName)
	existing, err := os.ReadFile(runtimeAbs)
	if err != nil || strings.TrimSpace(string(existing)) != strings.TrimSpace(string(mergedBytes)) {
		_ = fs.WriteAtomic(runtimeAbs, mergedBytes)
	}

	if tunDev, ok := extracted["tun_device"]; ok {            
		a.State.SetActualTunDevice(tunDev)
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
