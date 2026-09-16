package app

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"mihomo-tray/internal/core"
	"mihomo-tray/internal/state"
	"mihomo-tray/internal/view"
	"mihomo-tray/internal/tray"
	"mihomo-tray/internal/webui"
)

func (a *Application) applyConfigTransaction(ctx context.Context, targetRelPath string) error {
	_, extracted, err := a.Cfg.PrepareYAMLForPath(targetRelPath)
	if err != nil {
		return fmt.Errorf("生成运行时配置失败: %w", err)
	}

	runtimeAbs := filepath.Join(a.Cfg.BaseDir(), core.RuntimeConfigName)
	isKernelRunning := a.State.GetPhase() == state.PhaseRunning

	if isKernelRunning {
		reqCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()

		payload := map[string]interface{}{"path": filepath.ToSlash(runtimeAbs)}
		_, err := a.API.DoRequest(reqCtx, "PUT", "/configs?force=true", payload)

		if err != nil {
			logMsg := fmt.Errorf("热重载被内核拒绝 | 配置: %s | 原因: %v", targetRelPath, err)
			a.Kernel.WriteCoreLog("ERROR", logMsg.Error())
			slog.Error("配置应用失败，事务已回滚", "target", targetRelPath)
			return err
		}
		slog.Info("内核热重载接受配置，事务提交准备就绪")
	} else {
		slog.Info("内核处于停止状态，准备使用新配置唤醒")
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

func (a *Application) executeRemoteUpdate(ctx context.Context, targetRelPath string, isManual bool) {
	if !a.State.TryAcquireProfileLock(targetRelPath) {
		if isManual {
			slog.Warn("该订阅正在后台更新，已拦截重复操作", "path", targetRelPath)
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
			view.ShowErrorMessage("订阅更新拦截", err.Error())
		}
		slog.Error("订阅更新终止", "path", targetRelPath, "err", err)
		return
	}

	if success {
		slog.Info("订阅更新已完成", "path", targetRelPath)
		if a.Cfg.GetActivePath() == targetRelPath {
			slog.Info("活跃配置发生变更，触发内核热重载")
			_ = a.applyConfigTransaction(context.Background(), targetRelPath)
		}
		a.pushUIState()
	}
}

func (a *Application) ReloadConfig(ctx context.Context) {
	if a.State.IsReloading() {
		return
	}
	slog.Info("开始执行配置重载事务")
	a.State.SetReloading(true)

	go func() {
		defer a.State.SetReloading(false)
		defer a.pushUIState()

		target := a.Cfg.GetActivePath()

		if err := a.Cfg.ValidatePhysicalFile(target); err != nil {
			view.ShowErrorMessage("重载失败", "配置文件不存在或损坏，请检查文件")
			return
		}

		if err := a.applyConfigTransaction(ctx, target); err != nil {
			view.ShowErrorMessage("重载失败", "当前配置文件存在错误，请检查：\n\n"+err.Error())
		} else {
			a.restartWebUIIfOpen()
		}
	}()
}

func (a *Application) SyncRuntimeConfig() {
	activePath := a.Cfg.GetActivePath()
	if activePath == "" {
		return
	}

	if err := a.Cfg.ValidatePhysicalFile(activePath); err != nil {
		slog.Warn("底稿校验失败，已自动剥离失效配置", "path", activePath, "err", err)
		a.Cfg.SetActiveProfile("")
		return
	}

	if _, extracted, err := a.Cfg.PrepareYAMLForPath(activePath); err != nil {
		slog.Error("自动同步运行时配置失败", "err", err)
	} else if len(extracted) > 0 {
		a.Cfg.UpdateBatch(extracted)
	}
}

func (a *Application) RestartKernel() {
	slog.Info("正在重启内核进程")
	a.State.SetRestarting(true)
	a.State.SetReloading(false)
	a.State.SetPhase(state.PhaseInitializing)
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
		slog.Debug("检测到 Web 面板原先处于活跃状态，等待内核就绪后拉起新环境")

		go func() {
			for i := 0; i < 50; i++ {
				if a.State.IsExiting() {
					return
				}

				if a.State.GetPhase() == state.PhaseRunning {
					slog.Debug("内核已就绪，正在自动重新拉起 Web 面板")
					select {
					case a.UICommandCh <- tray.UICommand{Action: "OpenWebUI"}:
					default:
					}
					return
				}

				time.Sleep(200 * time.Millisecond)
			}
			slog.Warn("等待内核就绪超时，自动拉起 Web 面板失败")
		}()
	}
}
