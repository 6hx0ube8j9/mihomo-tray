package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"mihomo-tray/internal/config"
	"mihomo-tray/internal/core"
	"mihomo-tray/internal/domain"
)

func (a *Application) onProfileImported(ctx context.Context, newProfilePath string) {
	if len(a.Cfg.GetConfig().Profiles) == 1 {
		slog.Info("首个配置导入成功，触发全局自动激活并加载", "path", newProfilePath)
		_ = a.applyConfigTransaction(ctx, newProfilePath)
	}
	a.pushUIState()
}

func (a *Application) ImportLocalProfile(ctx context.Context, sourceFilePath string) error {
	if sourceFilePath == "" {
		return nil
	}

	slog.Info("开始导入本地配置", "source", sourceFilePath)

	newRelPath, err := a.Cfg.AddLocalProfile(sourceFilePath)
	if err != nil {
		slog.Error("本地配置导入失败", "err", err)
		return fmt.Errorf("无法读取或校验本地配置文件：\n\n%w", err)
	}

	a.onProfileImported(ctx, newRelPath)
	return nil
}

func (a *Application) AddRemoteProfile(ctx context.Context, payload string) error {
	parts := strings.SplitN(payload, "|", 4)
	if len(parts) != 4 {
		return fmt.Errorf("无效的订阅参数格式")
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
	
	return a.UpdateRemoteProfile(ctx, targetRelPath, true, !exists)
}

func (a *Application) UpdateRemoteProfile(ctx context.Context, targetRelPath string, isManual bool, isNew bool) error {
	if !a.State.TryAcquireProfileLock(targetRelPath) {
		if isManual {
			slog.Warn("拦截重复更新请求", "path", targetRelPath)
		}
		return nil
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
		slog.Error("更新配置失败", "path", targetRelPath, "err", err)
		if isNew {
			slog.Info("清理拉取失败的新建订阅文件", "path", targetRelPath)
			absPath := filepath.Join(a.Cfg.BaseDir(), filepath.FromSlash(targetRelPath))
			_ = os.Remove(absPath)
			a.Cfg.RemoveProfile(targetRelPath)
			a.ForcePushUIState()
		}
		if isManual {
			return fmt.Errorf("无法拉取最新的订阅配置，请检查网络或链接状态。\n\n详情：%w", err)
		}
		return nil
	}

	if success {
		slog.Info("更新配置成功", "path", targetRelPath)
		if a.Cfg.GetActivePath() == targetRelPath {
			slog.Info("当前活跃配置已更新，执行底层重载")
			_ = a.applyConfigTransaction(context.Background(), targetRelPath)
			a.pushUIState()
		} else if isNew {
			a.onProfileImported(ctx, targetRelPath)
		}
	}
	return nil
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

func (a *Application) SwitchProfile(ctx context.Context, targetPath string) error {
	if targetPath != "" && targetPath == a.Cfg.GetActivePath() {
		slog.Debug("配置已在使用中，忽略重复切换", "path", targetPath)
		a.ForcePushUIState()
		return nil
	}

	if a.State.IsProfileSwitching() {
		a.ForcePushUIState()
		return nil
	}
	a.State.SetProfileSwitching(true)

	isTransactionFailed := false
	defer func() {
		a.State.SetProfileSwitching(false)
		if isTransactionFailed {
			slog.Debug("配置切换失败，恢复原状态并刷新界面")
			a.ForcePushUIState()
		} else {
			a.pushUIState()
		}
	}()

	target := targetPath
	if target == "" {
		target = a.Cfg.GetActivePath()
	}

	if err := a.Cfg.ValidatePhysicalFile(target); err != nil {
		isTransactionFailed = true
		return fmt.Errorf("目标配置异常，请求已取消。\n\n错误: %w", err)
	}

	exePath := core.GetKernelPath(a.Cfg.BaseDir())
	absPath := filepath.Join(a.Cfg.BaseDir(), filepath.FromSlash(target))
	if err := core.ValidateConfig(exePath, a.Cfg.BaseDir(), absPath); err != nil {
		isTransactionFailed = true
		return fmt.Errorf("该配置存在严重错误，拒绝加载。\n\n错误: %w", err)
	}

	oldActive := a.Cfg.GetActivePath()
	a.Cfg.SetActiveProfile(target)
	a.pushUIState()

	if err := a.applyConfigTransaction(context.Background(), target); err != nil {
		a.Cfg.SetActiveProfile(oldActive)
		isTransactionFailed = true
		return fmt.Errorf("配置加载失败，已自动恢复原配置。\n\n错误: %w", err)
	} 
	
	a.restartWebUIIfOpen()
	return nil
}

func (a *Application) DeleteProfile(targetPath string) {
	absPath := filepath.Join(a.Cfg.BaseDir(), filepath.FromSlash(targetPath))
	if err := os.Remove(absPath); err != nil && !os.IsNotExist(err) {
		slog.Warn("清理本地物理文件失败", "path", absPath, "err", err)
	}

	a.Cfg.RemoveProfile(targetPath)
	a.pushUIState()
}

func (a *Application) MoveProfileUp(targetPath string) {
	a.Cfg.MoveProfile(targetPath, -1)
}

func (a *Application) MoveProfileDown(targetPath string) {
	a.Cfg.MoveProfile(targetPath, 1)
}
