package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"mihomo-tray/internal/core"
	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/sys"
	"mihomo-tray/internal/ui"
	"mihomo-tray/internal/webui"
)

func (a *Application) applyConfigTransaction(ctx context.Context, targetRelPath string) error {
	wantTun := a.Cfg.Get("tun") == "true"
	if wantTun && !sys.IsAdmin() {
		slog.Warn("非管理员权限无法开启 TUN，已自动关闭")
		wantTun = false
		a.Cfg.Set("tun", "false")
	}

	params := core.BuilderParams{
		Mode:    a.Cfg.Get("mode"),
		Tun:     wantTun,
		BaseDir: a.Cfg.BaseDir(),
		RelPath: targetRelPath,
	}

	_, extracted, err := core.BuildRuntimeYAML(params)
	if err != nil {
		return fmt.Errorf("生成运行时配置失败: %w", err)
	}

	runtimeAbs := filepath.Join(a.Cfg.BaseDir(), core.RuntimeConfigName)
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
		apiAddr, apiSecret := a.Cfg.ResolveKernelEndpoint(runtimeAbs)
		a.API.SetEndpoint(apiAddr, apiSecret)
	}

	a.Cfg.SetActiveProfile(targetRelPath)
	if len(extracted) > 0 {
		a.Cfg.UpdateBatch(extracted)
	}
	a.syncSystemProxy()

	if !isKernelRunning {
		a.Kernel.WakeDaemon()
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

	port := a.Cfg.Get("port")
	success, err := a.Cfg.UpgradeSubscription(targetRelPath, port, validator)

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
		defer a.ForcePushUIState()

		target := a.Cfg.GetActivePath()

		if err := a.safePreflightCheck(target, "重载配置"); err != nil {
			return
		}

		if err := a.applyConfigTransaction(ctx, target); err != nil {
			ui.ShowErrorMessage(nil, "配置重载失败", "内核拒绝加载当前配置文件，请检查语法：\n\n"+err.Error())
		} else {
			a.restartWebUIIfOpen()
		}
	}()
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

	wantTun := a.Cfg.Get("tun") == "true"
	if wantTun && !sys.IsAdmin() {
		slog.Warn("非管理员权限无法开启 TUN，已自动关闭")
		wantTun = false
		a.Cfg.Set("tun", "false")
	}

	params := core.BuilderParams{
		Mode:    a.Cfg.Get("mode"),
		Tun:     wantTun,
		BaseDir: a.Cfg.BaseDir(),
		RelPath: activePath,
	}

	if _, extracted, err := core.BuildRuntimeYAML(params); err != nil {
		slog.Error("同步运行配置失败", "err", err)
	} else if len(extracted) > 0 {
		a.Cfg.UpdateBatch(extracted)
	}
}

func (a *Application) RestartKernel() {
	slog.Info("正在重启内核进程")
	a.State.SetRestarting(true)
	a.State.SetReloading(false)
	a.State.SetPhase(domain.PhaseInitializing)
	a.Kernel.HaltDaemon()
	a.SyncRuntimeConfig()

	runtimeAbs := filepath.Join(a.Cfg.BaseDir(), core.RuntimeConfigName)
	apiAddr, apiSecret := a.Cfg.ResolveKernelEndpoint(runtimeAbs)

	a.API.SetEndpoint(apiAddr, apiSecret)

	if a.Cfg.Get("tun") == "true" {
		a.State.SetTunRequestedTime(time.Now())
	}

	a.Kernel.WakeDaemon()
	a.pushUIState()

	a.restartWebUIIfOpen()
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
					slog.Debug("内核已就绪，正在自动重新拉起 Web 面板")

					a.OpenWebUI()

					return
				}
				time.Sleep(200 * time.Millisecond)
			}
			slog.Warn("等待内核就绪超时，自动拉起 Web 面板失败")
		}()
	}
}
