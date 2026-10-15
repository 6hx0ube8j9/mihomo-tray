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
		return err
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

	a.ForcePushUIState()

	defer func() {
		a.State.EndAction()
		a.ForcePushUIState()
	}()

	target := targetPath
	if target == "" {
		target = oldActive
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
			return err
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
		a.FallbackToEmptyProfile(context.Background(), "正在使用的配置文件已被删除")
		return nil
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
			return err
		}
		slog.Warn("自动更新订阅失败", "path", targetRelPath, "err", err)
		return nil
	}

	if isManual && a.Cfg.GetActivePath() == targetRelPath {
		if !a.State.TryBeginAction(state.ActionReload) {
			return errors.New("系统繁忙，请稍后重试")
		}
		defer a.State.EndAction()

		if err := a.applyActiveConfig(ctx, "应用订阅更新"); err != nil {
			a.ForcePushUIState()
			return err
		}
	}

	slog.Info("订阅更新成功", "path", targetRelPath, "manual", isManual)
	a.ForcePushUIState()
	return nil
}

func (a *Application) FallbackToEmptyProfile(ctx context.Context, reason string) {
	slog.Warn("配置失效或被移除，安全回退为空配置", "reason", reason)

	a.Cfg.SetActiveProfile("")
	a.disableSystemProxySetting()

	a.Cfg.Update(func(c *domain.TrayConfig) {
		c.Config.Tun.Enable = false
	})
	a.State.SetTunRequestedTime(time.Time{})

	if a.State.GetPhase() == domain.PhaseRunning {
		_ = a.DeployAndApplyConfig(ctx, "", "回退空配置")
	}

	a.ForcePushUIState()
}

func (a *Application) getActiveProxyPort() string {
	if a.State.GetPhase() == domain.PhaseRunning && !a.Kernel.IsPaused() {
		return a.Cfg.GetEffectiveLocalProxyPortStr()
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
		return errors.New("订阅内容为空")
	}

	if targetRelPath == a.Cfg.GetActivePath() {
		if _, err := core.ValidateRuntimeYAML(a.Cfg.GetConfig(), tempBytes, a.Cfg.BaseDir()); err != nil {
			return fmt.Errorf("订阅校验失败: %w", err)
		}
	} else {
		if _, err := core.ComposeRuntimeYAML(a.Cfg.GetConfig(), tempBytes); err != nil {
			return fmt.Errorf("订阅语法错误: %w", err)
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

	fileName := filepath.Base(sourcePath)

	fi, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%s: 文件不存在", fileName)
		}
		return fmt.Errorf("%s: 无法读取文件: %w", fileName, err)
	}

	if fi.Size() == 0 {
		return fmt.Errorf("%s: 文件内容为空", fileName)
	}
	if fi.Size() > domain.MaxProfileBytes {
		return fmt.Errorf("%s: 文件大小超出限制 (最大 15MB)", fileName)
	}

	content, err := os.ReadFile(absPath)
	if err != nil {
		return fmt.Errorf("%s: 读取内容失败: %w", fileName, err)
	}

	if _, err := core.ComposeRuntimeYAML(a.Cfg.GetConfig(), content); err != nil {
		return fmt.Errorf("%s: %w", fileName, err)
	}

	return nil
}

func (a *Application) onProfileImported(ctx context.Context, newProfilePath string) {
	if len(a.Cfg.GetProfiles()) == 1 {
		slog.Info("激活首个配置", "path", newProfilePath)
		a.Cfg.SetActiveProfile(newProfilePath)
		a.ForcePushUIState()

		if err := a.applyActiveConfig(ctx, "激活首个配置"); err != nil {
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
