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

	"mihomo-tray/internal/core"
	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/state"
)

// ==================== 一、 配置切换与激活事务 ====================

// SwitchProfile 以原子事务方式切换当前激活的配置，包含物理校验、内核沙盒预检与失败回滚
func (a *Application) SwitchProfile(ctx context.Context, targetPath string) error {
	currentActive := a.Cfg.GetActivePath()
	if targetPath != "" && targetPath == currentActive {
		a.ForcePushUIState()
		return nil
	}

	if !a.State.TryBeginAction(state.ActionSwitchProfile) {
		a.ForcePushUIState()
		return fmt.Errorf("系统正在执行其他任务，请稍后重试")
	}
	defer func() {
		a.State.EndAction()
		a.ForcePushUIState()
	}()

	target := targetPath
	if target == "" {
		target = currentActive
	}

	// 1. 第一阶段：沙盒预检与物理校验 (零副作用拦截，确保当前配置不受影响)
	if target != "" {
		if err := a.Cfg.ValidatePhysicalFile(target); err != nil {
			return fmt.Errorf("目标配置文件无效: %w", err)
		}
		if err := a.validateProfileWithKernel(target); err != nil {
			return err
		}
	}

	// 2. 第二阶段：内核预检通过后，持久化激活项并应用
	oldActive := currentActive
	a.Cfg.SetActiveProfile(target)

	if err := a.applyActiveConfig(ctx, "切换配置"); err != nil {
		// 3. 第三阶段：异常自动回滚兜底 (防止内核与磁盘状态分裂)
		slog.Error("应用新配置失败，正在自动回滚原配置", "failedTarget", target, "rollbackTo", oldActive, "err", err)
		a.Cfg.SetActiveProfile(oldActive)
		_ = a.applyActiveConfig(context.Background(), "回滚原配置")
		return fmt.Errorf("内核加载新配置失败，系统已自动保留原配置运行:\n\n%w", err)
	}

	a.restartWebUIIfOpen()
	return nil
}

func (a *Application) onProfileImported(ctx context.Context, newProfilePath string) {
	if len(a.Cfg.GetProfiles()) == 1 {
		slog.Info("首个配置导入成功，已自动设为当前配置并启动", "path", newProfilePath)
		a.Cfg.SetActiveProfile(newProfilePath)
		a.ForcePushUIState()

		if err := a.applyActiveConfig(ctx, "自动激活首次导入的配置"); err != nil {
			slog.Warn("首次激活配置启动失败", "err", err)
		}

		a.restartWebUIIfOpen()
	} else {
		a.ForcePushUIState()
	}
}

// ==================== 二、 配置生命周期管理 (CRUD) ====================

func (a *Application) ImportLocalProfile(ctx context.Context, sourcePath string) error {
	if sourcePath == "" {
		return nil
	}

	if !a.State.TryBeginAction(state.ActionSwitchProfile) {
		return fmt.Errorf("系统正在处理其他配置任务，请稍后重试")
	}
	defer func() {
		a.State.EndAction()
		a.ForcePushUIState()
	}()

	slog.Info("开始导入本地配置文件", "path", sourcePath)

	if err := a.validateProfileSource(sourcePath); err != nil {
		return fmt.Errorf("配置文件格式校验失败: %w", err)
	}

	targetRelPath, _, err := a.Cfg.SafeCopyUntrustedConfig(sourcePath)
	if err != nil {
		return fmt.Errorf("配置文件保存失败: %w", err)
	}

	a.Cfg.RegisterNewProfile(targetRelPath)
	a.onProfileImported(ctx, targetRelPath)
	return nil
}

func (a *Application) AddRemoteProfile(ctx context.Context, rawName, url string, interval int) error {
	cleanURL := strings.TrimSpace(url)
	if cleanURL == "" {
		return fmt.Errorf("订阅链接不能为空")
	}

	if rawName == "" {
		rawName = fmt.Sprintf("%d", time.Now().Unix())
	}
	safeName := strings.ReplaceAll(rawName, "/", "_")
	fileName := fmt.Sprintf("%s.yaml", safeName)
	targetRelPath := filepath.ToSlash(filepath.Join(domain.ProfilesDir, fileName))

	if _, exists := a.Cfg.GetProfileByPath(targetRelPath); exists {
		return fmt.Errorf("已存在同名配置，请修改配置名称后重试")
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

func (a *Application) EditLocalProfile(ctx context.Context, targetPath, newName string) error {
	p, ok := a.Cfg.GetProfileByPath(targetPath)
	if !ok {
		return fmt.Errorf("找不到指定的配置文件")
	}
	if p.Name == newName {
		return nil
	}

	p.Name = newName
	a.Cfg.UpsertProfile(p)
	a.ForcePushUIState()
	return nil
}

func (a *Application) EditRemoteProfile(ctx context.Context, oldPath, newName, newURL string, newInterval int) error {
	p, ok := a.Cfg.GetProfileByPath(oldPath)
	if !ok {
		return fmt.Errorf("找不到指定的配置文件")
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
		slog.Info("当前活跃配置链接已更新，重新加载内核配置")
		if err := a.applyActiveConfig(ctx, "更新订阅并重新加载配置"); err != nil {
			a.ForcePushUIState()
			return fmt.Errorf("订阅文件已更新，但应用到内核失败: %w", err)
		}
	}

	a.ForcePushUIState()
	return nil
}

func (a *Application) DeleteProfile(targetPath string) error {
	isActive := targetPath == a.Cfg.GetActivePath()

	if err := a.Cfg.DeleteProfile(targetPath); err != nil {
		slog.Warn("清理本地配置文件出现异常", "path", targetPath, "err", err)
	}

	if isActive {
		slog.Info("当前活跃配置已被删除，重置为空配置运行状态")
		if err := a.applyActiveConfig(context.Background(), "重置为空配置"); err != nil {
			a.ForcePushUIState()
			return fmt.Errorf("配置已删除，但重置内核状态失败: %w", err)
		}
	}

	a.ForcePushUIState()
	return nil
}

// ==================== 三、 远程订阅拉取与同步 ====================

func (a *Application) UpdateRemoteProfile(ctx context.Context, targetRelPath string, isManual bool) error {
	if !a.State.TryAcquireProfileLock(targetRelPath) {
		if isManual {
			slog.Debug("忽略重复的订阅更新请求", "path", targetRelPath)
		}
		return nil
	}
	defer a.State.ReleaseProfileLock(targetRelPath)

	p, ok := a.Cfg.GetProfileByPath(targetRelPath)
	if !ok || p.URL == "" {
		if isManual {
			return fmt.Errorf("未找到该配置或该配置未包含有效的订阅链接")
		}
		return nil
	}

	err := a.fetchAndCommitRemoteProfile(ctx, targetRelPath, p.URL, &p)
	if err != nil {
		if isManual {
			return fmt.Errorf("拉取订阅失败，请检查网络连接或链接地址: %w", err)
		}
		slog.Warn("后台自动更新订阅失败", "path", targetRelPath, "err", err)
		return nil
	}

	slog.Info("订阅更新成功", "path", targetRelPath)

	if a.Cfg.GetActivePath() == targetRelPath {
		slog.Info("当前活跃配置已更新，重新加载配置")
		if err := a.applyActiveConfig(ctx, "应用订阅更新"); err != nil {
			a.ForcePushUIState()
			if isManual {
				return fmt.Errorf("订阅文件已更新，但重新加载内核失败: %w", err)
			}
			slog.Error("自动更新订阅后应用内核失败", "err", err)
		}
	}

	a.ForcePushUIState()
	return nil
}

func (a *Application) fetchAndCommitRemoteProfile(ctx context.Context, targetRelPath, url string, item *domain.ProfileItem) error {
	proxyPort := a.getActiveProxyPort()

	res, err := a.Cfg.FetchRemoteProfile(ctx, url, proxyPort)
	if err != nil {
		return err
	}
	defer os.Remove(res.TempPath)

	if err := a.validateProfileSource(res.TempPath); err != nil {
		return fmt.Errorf("订阅文件格式校验失败，已取消保存: %w", err)
	}

	// 若更新的是当前正在运行的配置，必须先进行内核沙盒预检，预检通过才允许覆盖落盘
	if targetRelPath == a.Cfg.GetActivePath() {
		tempContent, err := os.ReadFile(res.TempPath)
		if err != nil {
			return fmt.Errorf("读取临时订阅文件失败: %w", err)
		}

		if err := a.testRuntimeConfigContent(tempContent, filepath.Base(targetRelPath)); err != nil {
			a.Kernel.WriteCoreLog(domain.LogTagProfileUpdate, fmt.Sprintf("活跃订阅更新预检失败，放弃覆盖本地文件 [%s]:\n%v", filepath.Base(targetRelPath), err))
			return fmt.Errorf("远程订阅存在内核不支持的配置规则，已自动保留原版本: %w", err)
		}
	}

	item.Upload = res.Upload
	item.Download = res.Download
	item.Total = res.Total
	item.Expire = res.Expire
	item.LastUpdate = time.Now().Unix()

	return a.Cfg.CommitRemoteProfile(res.TempPath, targetRelPath, *item)
}

func (a *Application) getActiveProxyPort() string {
	if a.State.GetPhase() == domain.PhaseRunning && !a.Kernel.IsPaused() {
		return a.Cfg.GetEffectiveMixedPortStr()
	}
	return ""
}

// ==================== 四、 沙盒预检与配置校验 ====================

func (a *Application) validateProfileWithKernel(targetRelPath string) error {
	if targetRelPath == "" {
		return nil
	}

	absPath := a.Cfg.GetProfileAbsPath(targetRelPath)
	content, err := os.ReadFile(absPath)
	if err != nil {
		return fmt.Errorf("读取配置文件失败: %w", err)
	}

	return a.testRuntimeConfigContent(content, filepath.Base(targetRelPath))
}

// testRuntimeConfigContent 统一合成运行配置并在沙盒临时文件中执行内核级校验
func (a *Application) testRuntimeConfigContent(content []byte, logTag string) error {
	composeRes, err := core.ComposeRuntimeYAML(a.Cfg.GetConfig(), content)
	if err != nil {
		a.Kernel.WriteCoreLog(domain.LogTagConfig, fmt.Sprintf("配置合成语法校验失败 [%s]:\n%v", logTag, err))
		return fmt.Errorf("配置文件语法或规则存在错误: %w", err)
	}

	testConfigPath := filepath.Join(a.Cfg.BaseDir(), domain.TestConfigFileName)
	if err := os.WriteFile(testConfigPath, composeRes.YAML, 0644); err != nil {
		return fmt.Errorf("生成测试配置文件失败: %w", err)
	}
	defer os.Remove(testConfigPath)

	kernelPath := core.GetKernelPath(a.Cfg.BaseDir())
	if err := core.ValidateConfig(kernelPath, a.Cfg.BaseDir(), testConfigPath); err != nil {
		a.Kernel.WriteCoreLog(domain.LogTagConfig, fmt.Sprintf("内核校验配置文件失败 [%s]:\n%v", logTag, err))
		return fmt.Errorf("内核拒绝加载该配置 (配置项不受支持或格式异常):\n\n%w", err)
	}

	return nil
}

func (a *Application) validateProfileSource(sourcePath string) error {
	absPath := sourcePath
	if !filepath.IsAbs(sourcePath) {
		absPath = a.Cfg.GetProfileAbsPath(sourcePath)
	}

	fi, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("配置文件不存在")
		}
		return fmt.Errorf("读取配置文件失败: %w", err)
	}
	if fi.Size() == 0 {
		return fmt.Errorf("配置文件内容为空")
	}
	if fi.Size() > domain.MaxProfileBytes {
		return fmt.Errorf("配置文件大小超过系统限制 (最大允许 %d MB)", domain.MaxProfileBytes/(1024*1024))
	}

	content, err := os.ReadFile(absPath)
	if err != nil {
		return fmt.Errorf("读取配置文件内容失败: %w", err)
	}

	if _, err := core.ComposeRuntimeYAML(a.Cfg.GetConfig(), content); err != nil {
		a.Kernel.WriteCoreLog(domain.LogTagConfig, fmt.Sprintf("配置文件语法校验失败 [%s]:\n%v", filepath.Base(sourcePath), err))
		return fmt.Errorf("配置文件存在语法或规则错误: %w", err)
	}

	return nil
}

// ==================== 五、 视图查询与排序辅助 ====================

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
