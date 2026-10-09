package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"mihomo-tray/internal/core"
	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/state"
)

func (a *Application) ImportLocalProfile(ctx context.Context, sourcePath string) error {
	if sourcePath == "" {
		return nil
	}

	if !a.State.TryBeginAction(state.ActionSwitchProfile) {
		return errors.New("系统繁忙，请稍后重试")
	}
	defer func() {
		a.State.EndAction()
		a.ForcePushUIState()
	}()

	slog.Info("导入本地配置", "path", sourcePath)

	if err := a.validateProfileSource(sourcePath); err != nil {
		return fmt.Errorf("配置格式错误: %w", err)
	}

	targetRelPath, _, err := a.Cfg.SafeCopyUntrustedConfig(sourcePath)
	if err != nil {
		return fmt.Errorf("保存配置失败: %w", err)
	}

	a.Cfg.RegisterNewProfile(targetRelPath)
	a.onProfileImported(ctx, targetRelPath)
	return nil
}

func (a *Application) AddRemoteProfile(ctx context.Context, rawName, url string, interval int) error {
	cleanURL := strings.TrimSpace(url)
	if cleanURL == "" {
		return errors.New("订阅链接为空")
	}

	safeName, targetRelPath, err := a.Cfg.AllocateRemoteProfilePath(rawName)
	if err != nil {
		return err
	}

	newItem := domain.ProfileItem{
		Name:       safeName,
		Path:       targetRelPath,
		URL:        cleanURL,
		AutoUpdate: interval > 0,
		Interval:   interval,
	}

	if err := a.fetchAndCommitRemoteProfile(ctx, targetRelPath, newItem.URL, &newItem); err != nil {
		return err
	}

	slog.Info("添加远程订阅成功", "path", targetRelPath)
	a.onProfileImported(ctx, targetRelPath)
	return nil
}

func (a *Application) SwitchProfile(ctx context.Context, targetPath string) error {
	oldActive := a.Cfg.GetActivePath()
	if targetPath != "" && targetPath == oldActive {
		a.ForcePushUIState()
		return nil
	}

	if !a.State.TryBeginAction(state.ActionSwitchProfile) {
		a.ForcePushUIState()
		return errors.New("系统繁忙，请稍后重试")
	}
	defer func() {
		a.State.EndAction()
		a.ForcePushUIState()
	}()

	target := targetPath
	if target == "" {
		target = oldActive
	}

	if target != "" {
		if err := a.Cfg.ValidatePhysicalFile(target); err != nil {
			return fmt.Errorf("配置文件错误: %w", err)
		}
	}

	if err := a.DeployAndApplyConfig(ctx, target, "切换配置"); err != nil {
		return err
	}

	a.Cfg.SetActiveProfile(target)

	a.restartWebUIIfOpen()
	return nil
}

func (a *Application) EditRemoteProfile(ctx context.Context, oldPath, newName, newURL string, newInterval int) error {
	p, ok := a.Cfg.GetProfileByPath(oldPath)
	if !ok {
		return errors.New("配置不存在")
	}

	if newName == p.Name && newURL == p.URL && newInterval == p.Interval {
		return nil
	}

	urlChanged := (newURL != p.URL)

	if urlChanged {
		if err := a.fetchAndCommitRemoteProfile(ctx, p.Path, newURL, &p); err != nil {
			return err
		}
	}

	p.Name = newName
	p.URL = newURL
	p.Interval = newInterval
	p.AutoUpdate = newInterval > 0
	a.Cfg.UpsertProfile(p)

	if urlChanged && a.Cfg.GetActivePath() == p.Path {
		slog.Info("订阅地址变更，重新加载配置")
		if err := a.applyActiveConfig(ctx, "更新订阅"); err != nil {
			a.ForcePushUIState()
			return fmt.Errorf("应用配置失败: %w", err)
		}
	}

	a.ForcePushUIState()
	return nil
}

func (a *Application) EditLocalProfile(ctx context.Context, targetPath, newName string) error {
	p, ok := a.Cfg.GetProfileByPath(targetPath)
	if !ok {
		return errors.New("配置不存在")
	}
	if p.Name == newName {
		return nil
	}

	p.Name = newName
	a.Cfg.UpsertProfile(p)
	a.ForcePushUIState()
	return nil
}

func (a *Application) DeleteProfile(targetPath string) error {
	isActive := targetPath == a.Cfg.GetActivePath()

	if err := a.Cfg.DeleteProfile(targetPath); err != nil {
		slog.Warn("清理本地配置失败", "path", targetPath, "err", err)
	}

	if isActive {
		slog.Info("活跃配置已删除，切换空配置")
		if err := a.applyActiveConfig(context.Background(), "重置为空配置"); err != nil {
			a.ForcePushUIState()
			return fmt.Errorf("重置内核失败: %w", err)
		}
	}

	a.ForcePushUIState()
	return nil
}

func (a *Application) UpdateRemoteProfile(ctx context.Context, targetRelPath string, isManual bool) error {
	if !a.State.TryAcquireProfileLock(targetRelPath) {
		return nil
	}
	defer a.State.ReleaseProfileLock(targetRelPath)

	p, ok := a.Cfg.GetProfileByPath(targetRelPath)
	if !ok || p.URL == "" {
		if isManual {
			return errors.New("配置或订阅链接不存在")
		}
		return nil
	}

	err := a.fetchAndCommitRemoteProfile(ctx, targetRelPath, p.URL, &p)
	if err != nil {
		if isManual {
			return fmt.Errorf("订阅拉取失败: %w", err)
		}
		slog.Warn("自动更新订阅失败", "path", targetRelPath, "err", err)
		return nil
	}

	slog.Info("订阅更新成功", "path", targetRelPath)

	if a.Cfg.GetActivePath() == targetRelPath {
		slog.Info("活跃配置已更新，重新加载")
		if err := a.applyActiveConfig(ctx, "应用订阅更新"); err != nil {
			a.ForcePushUIState()
			if isManual {
				return fmt.Errorf("应用内核失败: %w", err)
			}
			slog.Error("自动更新订阅应用失败", "err", err)
		}
	}

	a.ForcePushUIState()
	return nil
}

func (a *Application) getActiveProxyPort() string {
	if a.State.GetPhase() == domain.PhaseRunning && !a.Kernel.IsPaused() {
		return a.Cfg.GetEffectiveMixedPortStr()
	}
	return ""
}

func (a *Application) fetchAndCommitRemoteProfile(ctx context.Context, targetRelPath, url string, item *domain.ProfileItem) error {
	proxyPort := a.getActiveProxyPort()

	res, err := a.Cfg.FetchRemoteProfile(ctx, url, proxyPort)
	if err != nil {
		return err
	}
	defer os.Remove(res.TempPath)

	tempBytes, err := os.ReadFile(res.TempPath)
	if err != nil || len(tempBytes) == 0 {
		return errors.New("订阅文件内容为空")
	}

	if targetRelPath == a.Cfg.GetActivePath() {
		if _, err := core.ValidateRuntimeYAML(a.Cfg.GetConfig(), tempBytes, a.Cfg.BaseDir()); err != nil {
			a.Kernel.WriteCoreLog(domain.LogTagProfileUpdate, fmt.Sprintf("活跃订阅预检失败 [%s]: %v", filepath.Base(targetRelPath), err))
			return errors.New("订阅配置校验失败，已保留原版本")
		}
	} else {
		if _, err := core.ComposeRuntimeYAML(a.Cfg.GetConfig(), tempBytes); err != nil {
			return errors.New("订阅配置语法错误")
		}
	}

	item.Upload = res.Upload
	item.Download = res.Download
	item.Total = res.Total
	item.Expire = res.Expire
	item.LastUpdate = time.Now().Unix()

	return a.Cfg.CommitRemoteProfile(res.TempPath, targetRelPath, *item)
}

func (a *Application) validateProfileSource(sourcePath string) error {
	absPath := sourcePath
	if !filepath.IsAbs(sourcePath) {
		absPath = a.Cfg.GetProfileAbsPath(sourcePath)
	}

	fi, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return errors.New("配置文件不存在")
		}
		return fmt.Errorf("读取文件错误: %w", err)
	}
	if fi.Size() == 0 {
		return errors.New("配置文件内容为空")
	}
	if fi.Size() > domain.MaxProfileBytes {
		return errors.New("配置超出大小限制")
	}

	content, err := os.ReadFile(absPath)
	if err != nil {
		return fmt.Errorf("读取配置内容失败: %w", err)
	}

	if _, err := core.ComposeRuntimeYAML(a.Cfg.GetConfig(), content); err != nil {
		a.Kernel.WriteCoreLog(domain.LogTagConfig, fmt.Sprintf("语法校验失败 [%s]: %v", filepath.Base(sourcePath), err))
		return fmt.Errorf("配置语法错误: %w", err)
	}

	return nil
}

func (a *Application) onProfileImported(ctx context.Context, newProfilePath string) {
	if len(a.Cfg.GetProfiles()) == 1 {
		slog.Info("激活首个导入配置", "path", newProfilePath)
		a.Cfg.SetActiveProfile(newProfilePath)
		a.ForcePushUIState()

		if err := a.applyActiveConfig(ctx, "激活首个导入配置"); err != nil {
			slog.Warn("首次激活配置失败", "err", err)
		}

		a.restartWebUIIfOpen()
	} else {
		a.ForcePushUIState()
	}
}

func (a *Application) GetProfileInfo(path string) (domain.ProfileItem, bool) {
	return a.Cfg.GetProfileByPath(path)
}

func (a *Application) MoveProfileUp(path string) {
	a.Cfg.MoveProfile(path, -1)
}

func (a *Application) MoveProfileDown(path string) {
	a.Cfg.MoveProfile(path, 1)
}

func (a *Application) SetProfileInterval(payload string) {
	parts := strings.SplitN(payload, "|", 2)
	if len(parts) != 2 {
		return
	}
	path := parts[0]
	interval, err := strconv.Atoi(parts[1])
	if err != nil {
		return
	}

	if p, ok := a.Cfg.GetProfileByPath(path); ok {
		p.Interval = interval
		p.AutoUpdate = interval > 0
		a.Cfg.UpsertProfile(p)
		a.ForcePushUIState()
	}
}
