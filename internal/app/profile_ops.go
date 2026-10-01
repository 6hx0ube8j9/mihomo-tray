package app

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"

	"mihomo-tray/internal/core"
	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/ui"
)

func (a *Application) ImportLocalProfile(ctx context.Context, sourceFilePath string) {
	if sourceFilePath == "" {
		return
	}

	slog.Info("开始导入本地配置", "source", sourceFilePath)

	newRelPath, err := a.Cfg.AddLocalProfile(sourceFilePath)
	if err != nil {
		slog.Error("本地配置导入失败", "err", err)
		ui.ShowErrorMessage(nil, "导入失败", "无法读取或校验本地配置文件：\n\n"+err.Error())
		return
	}

	a.onProfileImported(ctx, newRelPath)
}

func (a *Application) UpdateRemoteProfile(ctx context.Context, targetRelPath string, isManual bool, isNew bool) {
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
			ui.ShowErrorMessage(nil, "订阅更新失败", "无法拉取最新的订阅配置，请检查网络或链接状态。\n\n详情："+err.Error())
		}
		slog.Error("更新配置失败", "path", targetRelPath, "err", err)

		if isNew {
			slog.Info("清理无效的新建订阅文件", "path", targetRelPath)
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
			a.pushUIState()
		} else if isNew {
			a.onProfileImported(ctx, targetRelPath)
		}
	}
}

func (a *Application) onProfileImported(ctx context.Context, newProfilePath string) {
	if len(a.Cfg.GetConfig().Profiles) == 1 {
		slog.Info("首个配置导入成功，触发全局自动激活并加载", "path", newProfilePath)
		_ = a.applyConfigTransaction(ctx, newProfilePath)
	}

	a.pushUIState()
}
