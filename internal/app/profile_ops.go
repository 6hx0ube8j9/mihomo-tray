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


func (a *Application) AddRemoteProfile(ctx context.Context, payload string) {
	parts := strings.SplitN(payload, "|", 4)
	if len(parts) != 4 {
		return
	}

	interval, _ := strconv.Atoi(parts[2])
	rawName := strings.TrimSpace(parts[0])
	if rawName == "" {
		rawName = fmt.Sprintf("%d", time.Now().Unix())
	}

	safeName := strings.ReplaceAll(rawName, "/", "_")
	fileName := fmt.Sprintf("%s.yaml", safeName)
	targetRelPath := filepath.ToSlash(filepath.Join(config.ProfilesDir, fileName))

	newItem := domain.ProfileItem{
		Name:       safeName,
		Path:       targetRelPath,
		URL:        strings.TrimSpace(parts[1]),
		AutoUpdate: parts[3] == "true",
		Interval:   interval,
	}

	_, exists := a.Cfg.GetProfileByPath(targetRelPath)
	a.Cfg.UpsertProfile(newItem)
	
	a.UpdateRemoteProfile(ctx, targetRelPath, true, !exists)
}

func (a *Application) SetProfileInterval(payload string) {
	parts := strings.Split(payload, "|")
	if len(parts) != 2 {
		return
	}
	targetPath := parts[0]
	interval, err := strconv.Atoi(parts[1])
	if err != nil {
		return
	}

	if p, ok := a.Cfg.GetProfileByPath(targetPath); ok {
		p.Interval = interval
		p.AutoUpdate = interval > 0
		a.Cfg.UpsertProfile(p)
		slog.Info("修改订阅更新频率", "path", targetPath, "interval", interval)
		a.pushUIState() 
	}
}

func (a *Application) DeleteProfile(targetPath string) {
	if targetPath == a.Cfg.GetActivePath() {
		slog.Warn("拒绝删除当前正在使用的配置")
		return
	}

	if !ui.ShowConfirmMessage(nil, "确认删除", "确定要删除此配置文件吗？\n\n此操作不可恢复，本地文件将被同时删除。") {
		return
	}

	absPath := filepath.Join(a.Cfg.BaseDir(), filepath.FromSlash(targetPath))
	if err := os.Remove(absPath); err != nil && !os.IsNotExist(err) {
		slog.Warn("清理本地文件失败", "path", absPath, "err", err)
	}

	a.Cfg.RemoveProfile(targetPath)
	a.pushUIState()
}
