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
	"mihomo-tray/internal/state"
)

func (a *Application) ImportLocalProfile(ctx context.Context, sourcePath string) error {
	if sourcePath == "" {
		return nil
	}

	if err := a.Cfg.CheckProfileLimit(); err != nil {
		return err
	}

	if !a.State.TryBeginAction(state.ActionSwitchProfile) {
		return fmt.Errorf("系统正在处理其他配置操作，请稍后重试")
	}
	defer func() {
		a.State.EndAction()
		a.ForcePushUIState()
	}()

	slog.Debug("开始导入本地配置", "source", sourcePath)

	if err := a.validateProfileSource(sourcePath); err != nil {
		return fmt.Errorf("配置文件存在语法或规则错误。\n\n%w", err)
	}

	targetName, _, err := a.Cfg.SafeCopyUntrustedConfig(sourcePath)
	if err != nil {
		return fmt.Errorf("文件复制失败，请检查系统权限。\n\n%w", err)
	}

	a.Cfg.RegisterNewProfile(targetName)
	a.onProfileImported(ctx, targetName)

	return nil
}

func (a *Application) AddRemoteProfile(ctx context.Context, rawName, url string, interval int) error {
	if err := a.Cfg.CheckProfileLimit(); err != nil {
		return err
	}

	cleanURL := strings.TrimSpace(url)
	if cleanURL == "" {
		return fmt.Errorf("订阅链接不可为空")
	}

	if rawName == "" {
		rawName = fmt.Sprintf("%d", time.Now().Unix())
	}
	safeName := strings.ReplaceAll(rawName, "/", "_")
	fileName := fmt.Sprintf("%s.yaml", safeName)
	targetRelPath := filepath.ToSlash(filepath.Join(config.ProfilesDir, fileName))

	if _, exists := a.Cfg.GetProfileByPath(targetRelPath); exists {
		return fmt.Errorf("配置名称或路径已存在冲突，请重命名")
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

	slog.Info("添加并拉取订阅成功", "path", targetRelPath)
	a.onProfileImported(ctx, targetRelPath)
	return nil
}

func (a *Application) SwitchProfile(ctx context.Context, targetPath string) error {
	if targetPath != "" && targetPath == a.Cfg.GetActivePath() {
		a.ForcePushUIState()
		return nil
	}

	if !a.State.TryBeginAction(state.ActionSwitchProfile) {
		a.ForcePushUIState()
		return fmt.Errorf("系统正在处理其他核心任务，请稍后重试")
	}
	defer func() {
		a.State.EndAction()
		a.ForcePushUIState()
	}()

	target := targetPath
	if target == "" {
		target = a.Cfg.GetActivePath()
	}

	if target != "" {
		if err := a.Cfg.ValidatePhysicalFile(target); err != nil {
			return fmt.Errorf("配置文件丢失或损坏，已拦截切换。\n\n%w", err)
		}
	}

	deployRes, err := a.prepareAndValidateConfig(target)
	if err != nil {
		return err 
	}

	a.Cfg.SetActiveProfile(target)

	a.executeKernelTransition(ctx, func(c context.Context) error {
		return a.apiHotReloadCommand(c, deployRes.RuntimeAbs)
	}, "切换配置")

	a.restartWebUIIfOpen()
	return nil
}

func (a *Application) EditRemoteProfile(ctx context.Context, oldPath, newName, newURL string, newInterval int) error {
	p, ok := a.Cfg.GetProfileByPath(oldPath)
	if !ok {
		return fmt.Errorf("找不到指定的配置文件，可能已被删除")
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
		slog.Info("当前活跃配置链接已修改且拉取成功，执行内核下发")
		if err := a.applyActiveConfig(ctx, "订阅更新热加载"); err != nil {
			a.ForcePushUIState()
			return fmt.Errorf("订阅信息拉取成功，但应用至内核失败。\n\n%w", err)
		}
	}

	a.ForcePushUIState()
	return nil
}

func (a *Application) EditLocalProfile(ctx context.Context, targetPath, newName string) error {
	p, ok := a.Cfg.GetProfileByPath(targetPath)
	if !ok {
		return fmt.Errorf("找不到指定的配置文件，可能已被删除")
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

	absPath := filepath.Join(a.Cfg.BaseDir(), filepath.FromSlash(targetPath))
	if err := os.Remove(absPath); err != nil && !os.IsNotExist(err) {
		slog.Warn("清理本地物理文件受阻", "path", absPath, "err", err)
	}

	a.Cfg.RemoveProfile(targetPath)

	if isActive {
		slog.Info("当前活跃配置已被删除，重置内核进入空转状态")
		if err := a.applyActiveConfig(context.Background(), "空转重置"); err != nil {
			a.ForcePushUIState()
			return fmt.Errorf("配置已被删除，但系统进入空转状态时发生异常。\n\n%w", err)
		}
	}

	a.ForcePushUIState()
	return nil
}

func (a *Application) UpdateRemoteProfile(ctx context.Context, targetRelPath string, isManual bool) error {
	if !a.State.TryAcquireProfileLock(targetRelPath) {
		if isManual {
			slog.Debug("拦截到用户重复点击的更新请求", "path", targetRelPath)
		}
		return nil
	}
	defer a.State.ReleaseProfileLock(targetRelPath)

	p, ok := a.Cfg.GetProfileByPath(targetRelPath)
	if !ok || p.URL == "" {
		if isManual {
			return fmt.Errorf("配置文件不存在或尚未配置订阅链接")
		}
		return nil
	}

	err := a.fetchAndCommitRemoteProfile(ctx, targetRelPath, p.URL, &p)
	if err != nil {
		if isManual {
			return fmt.Errorf("无法拉取最新的订阅配置。\n\n%w", err)
		}
		slog.Warn("后台自动更新订阅失败", "path", targetRelPath, "err", err)
		return nil
	}

	slog.Info("配置更新成功", "path", targetRelPath)

	if a.Cfg.GetActivePath() == targetRelPath {
		slog.Info("当前活跃配置已更新，执行底层重载")
		if err := a.applyActiveConfig(ctx, "订阅更新重载"); err != nil {
			a.ForcePushUIState()
			if isManual {
				return fmt.Errorf("订阅更新成功，但应用内核时失败。\n\n%w", err)
			}
			slog.Error("自动更新订阅成功，但应用内核失败", "err", err)
		}
	}

	a.ForcePushUIState()
	return nil
}

func (a *Application) getActiveProxyPort() string {
	if a.State.GetPhase() == domain.PhaseRunning && !a.Kernel.IsPaused() {
		cfg := a.Cfg.GetConfig()
		port := a.Cfg.GetEffectivePort(cfg.Config.MixedPort, domain.DefaultMixedPort)
		return strconv.Itoa(port)
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

	if err := a.validateProfileSource(res.TempPath); err != nil {
		return fmt.Errorf("订阅文件内容存在语法或规则错误，已拒绝保存。\n\n%w", err)
	}

	item.Upload = res.Upload
	item.Download = res.Download
	item.Total = res.Total
	item.Expire = res.Expire
	item.LastUpdate = time.Now().Unix()

	return a.Cfg.CommitRemoteProfile(res.TempPath, targetRelPath, *item)
}

func (a *Application) validateProfileSource(sourcePath string) error {
	absPath := filepath.Join(a.Cfg.BaseDir(), filepath.FromSlash(sourcePath))
	if filepath.IsAbs(sourcePath) {
		absPath = sourcePath
	}

	fi, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("配置文件不存在")
		}
		return fmt.Errorf("读取配置文件状态失败: %w", err)
	}
	if fi.Size() == 0 {
		return fmt.Errorf("配置文件内容为空 (0 字节)")
	}
	if fi.Size() > domain.MaxProfileBytes {
		return fmt.Errorf("配置文件体积超出上限 (最大允许 %d MB)", domain.MaxProfileBytes/(1024*1024))
	}

	content, err := os.ReadFile(absPath)
	if err != nil {
		return fmt.Errorf("读取待校验配置失败: %w", err)
	}

	if _, err := core.ComposeRuntimeYAML(a.Cfg.GetConfig(), content); err != nil {
		a.Kernel.WriteCoreLog("CONFIG", fmt.Sprintf("底稿语法断言失败 [%s]:\n%v", filepath.Base(sourcePath), err))
		return fmt.Errorf("配置语法或规则存在错误，无法合成运行配置。\n\n%w", err)
	}

	return nil
}

func (a *Application) onProfileImported(ctx context.Context, newProfilePath string) {
	if len(a.Cfg.GetProfiles()) == 1 {
		slog.Info("首个配置导入成功，触发全局自动激活并加载", "path", newProfilePath)
		a.Cfg.SetActiveProfile(newProfilePath)
		a.ForcePushUIState()

		if err := a.applyActiveConfig(ctx, "首配置自动激活"); err != nil {
			slog.Warn("自动激活首个配置时遇到启动异常", "err", err)
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
