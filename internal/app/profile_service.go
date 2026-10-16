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

	"mihomo-tray/internal/config"
	"mihomo-tray/internal/core"
	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/state"
)

type ProfileService struct {
	cfg      *config.Manager
	st       *state.RuntimeState
	deployer *Deployer
	guard    *ProxyGuard
}

func NewProfileService(cfg *config.Manager, st *state.RuntimeState, deployer *Deployer, guard *ProxyGuard) *ProfileService {
	return &ProfileService{
		cfg:      cfg,
		st:       st,
		deployer: deployer,
		guard:    guard,
	}
}

func (s *ProfileService) EnsureActiveProfileValid(ctx context.Context) {
	activePath := s.cfg.GetActivePath()
	if activePath == "" {
		return
	}

	if err := s.cfg.ValidatePhysicalFile(activePath); err == nil {
		return
	}

	if p, ok := s.cfg.GetProfileByPath(activePath); ok && p.URL != "" {
		slog.Info("当前活跃配置物理文件丢失，尝试拉取远程订阅恢复", "path", activePath)
		fCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		err := s.fetchAndCommit(fCtx, activePath, p.URL, &p)
		cancel()

		if err == nil {
			slog.Info("配置已成功拉取恢复")
			return
		}
		slog.Warn("拉取恢复失败", "err", err)
	}

	s.FallbackToEmpty(ctx, "活跃配置物理文件无效且无法恢复")
}

func (s *ProfileService) FallbackToEmpty(ctx context.Context, reason string) {
	slog.Warn("配置失效，回退为空配置底稿", "reason", reason)

	s.cfg.Update(func(c *domain.TrayConfig) {
		c.Profiles.Active = ""
		bProxy := false
		c.General.SystemProxy = &bProxy
		c.Config.Tun.Enable = false
	})

	s.st.SetTunRequestedTime(time.Time{})
	s.st.SetTunAlive(false)

	if s.st.GetPhase() == domain.PhaseRunning {
		_ = s.deployer.DeployAndApply(ctx, "", "回退空配置")
	} else {
		cfg := s.cfg.GetConfig()
		_, _ = core.DeployRuntimeConfig(cfg, "", s.cfg.BaseDir())
	}
	s.guard.Sync()
}

func (s *ProfileService) SwitchProfile(ctx context.Context, targetPath string) error {
	if targetPath == "" {
		s.FallbackToEmpty(ctx, "用户主动切换为空配置")
		return nil
	}

	if targetPath == s.cfg.GetActivePath() {
		return nil
	}

	if !s.st.TryBeginAction(state.ActionSwitchProfile) {
		return errors.New("系统繁忙，请稍后重试")
	}
	defer s.st.EndAction()

	if err := s.deployer.DeployAndApply(ctx, targetPath, "切换配置"); err != nil {
		return err
	}

	s.cfg.SetActiveProfile(targetPath)
	return nil
}

func (s *ProfileService) ImportLocalProfile(ctx context.Context, sourcePath string) error {
	if sourcePath == "" {
		return nil
	}
	slog.Info("导入本地配置", "path", sourcePath)

	if err := s.validateProfileSource(sourcePath); err != nil {
		return err
	}

	targetRelPath, _, err := s.cfg.SafeCopyUntrustedConfig(sourcePath)
	if err != nil {
		return fmt.Errorf("保存配置失败: %w", err)
	}

	s.cfg.RegisterNewProfile(targetRelPath)
	
	if len(s.cfg.GetProfiles()) == 1 {
		return s.SwitchProfile(ctx, targetRelPath)
	}
	return nil
}

func (s *ProfileService) AddRemoteProfile(ctx context.Context, rawName, url string, interval int) error {
	cleanURL := strings.TrimSpace(url)
	if cleanURL == "" {
		return errors.New("订阅链接为空")
	}

	safeName, targetRelPath, err := s.cfg.AllocateRemoteProfilePath(rawName)
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

	if err := s.fetchAndCommit(ctx, targetRelPath, newItem.URL, &newItem); err != nil {
		return err
	}

	slog.Info("添加远程订阅成功", "path", targetRelPath)
	if len(s.cfg.GetProfiles()) == 1 {
		return s.SwitchProfile(ctx, targetRelPath)
	}
	return nil
}

func (s *ProfileService) UpdateRemoteProfile(ctx context.Context, targetRelPath string, isManual bool) error {
	if !s.st.TryAcquireProfileLock(targetRelPath) {
		return nil
	}
	defer s.st.ReleaseProfileLock(targetRelPath)

	p, ok := s.cfg.GetProfileByPath(targetRelPath)
	if !ok || p.URL == "" {
		if isManual {
			return errors.New("配置或订阅链接不存在")
		}
		return nil
	}

	err := s.fetchAndCommit(ctx, targetRelPath, p.URL, &p)
	if err != nil {
		if isManual {
			return err
		}
		slog.Warn("自动更新订阅失败", "path", targetRelPath, "err", err)
		return nil
	}

	slog.Info("订阅更新已落盘", "path", targetRelPath, "manual", isManual)

	if isManual && s.cfg.GetActivePath() == targetRelPath {
		if !s.st.TryBeginAction(state.ActionReload) {
			return errors.New("系统繁忙，配置已保存，请稍后手动重载")
		}
		defer s.st.EndAction()
		return s.deployer.DeployAndApply(ctx, targetRelPath, "应用手动订阅更新")
	}

	return nil
}

func (s *ProfileService) EditRemoteProfile(ctx context.Context, oldPath, newName, newURL string, newInterval int) error {
	p, ok := s.cfg.GetProfileByPath(oldPath)
	if !ok {
		return errors.New("配置不存在")
	}

	if newName == p.Name && newURL == p.URL && newInterval == p.Interval {
		return nil
	}

	urlChanged := newURL != p.URL
	if urlChanged {
		if err := s.fetchAndCommit(ctx, p.Path, newURL, &p); err != nil {
			return err
		}
	}

	p.Name = newName
	p.URL = newURL
	p.Interval = newInterval
	p.AutoUpdate = newInterval > 0
	s.cfg.UpsertProfile(p)

	if urlChanged && s.cfg.GetActivePath() == p.Path {
		if !s.st.TryBeginAction(state.ActionReload) {
			return errors.New("配置已保存，但系统繁忙未能立即热重载")
		}
		defer s.st.EndAction()
		return s.deployer.DeployAndApply(ctx, p.Path, "应用订阅地址变更")
	}
	return nil
}

func (s *ProfileService) EditLocalProfile(ctx context.Context, targetPath, newName string) error {
	p, ok := s.cfg.GetProfileByPath(targetPath)
	if !ok {
		return errors.New("配置不存在")
	}
	if p.Name != newName {
		p.Name = newName
		s.cfg.UpsertProfile(p)
	}
	return nil
}

func (s *ProfileService) DeleteProfile(ctx context.Context, targetPath string) error {
	isActive := targetPath == s.cfg.GetActivePath()

	if err := s.cfg.DeleteProfile(targetPath); err != nil {
		slog.Warn("清理本地配置失败", "path", targetPath, "err", err)
	}

	if isActive {
		slog.Info("活跃配置已被删除，执行降级回退")
		s.FallbackToEmpty(ctx, "正在使用的配置文件被删除")
	}
	return nil
}

func (s *ProfileService) GetProfileInfo(path string) (domain.ProfileItem, bool) {
	return s.cfg.GetProfileByPath(path)
}

func (s *ProfileService) MoveProfileUp(path string)   { s.cfg.MoveProfile(path, -1) }
func (s *ProfileService) MoveProfileDown(path string) { s.cfg.MoveProfile(path, 1) }

func (s *ProfileService) SetProfileInterval(payload string) {
	parts := strings.SplitN(payload, "|", 2)
	if len(parts) != 2 {
		return
	}
	path := parts[0]
	interval, err := strconv.Atoi(parts[1])
	if err != nil {
		return
	}

	if p, ok := s.cfg.GetProfileByPath(path); ok {
		p.Interval = interval
		p.AutoUpdate = interval > 0
		s.cfg.UpsertProfile(p)
	}
}

func (s *ProfileService) fetchAndCommit(ctx context.Context, targetRelPath, url string, item *domain.ProfileItem) error {
	proxyPort := s.cfg.GetEffectiveLocalProxyPortStr()
	res, err := s.cfg.FetchRemoteProfile(ctx, url, proxyPort)
	if err != nil {
		return err
	}
	defer os.Remove(res.TempPath)

	tempBytes, err := os.ReadFile(res.TempPath)
	if err != nil || len(tempBytes) == 0 {
		return errors.New("订阅内容为空")
	}

	if targetRelPath == s.cfg.GetActivePath() {
		if _, err := core.ValidateRuntimeYAML(s.cfg.GetConfig(), tempBytes, s.cfg.BaseDir()); err != nil {
			return fmt.Errorf("订阅配置内容校验失败: %w", err)
		}
	} else {
		if _, err := core.ComposeRuntimeYAML(s.cfg.GetConfig(), tempBytes); err != nil {
			return fmt.Errorf("订阅文件语法错误: %w", err)
		}
	}

	item.Upload = res.Upload
	item.Download = res.Download
	item.Total = res.Total
	item.Expire = res.Expire
	item.LastUpdate = time.Now().Unix()

	return s.cfg.CommitRemoteProfile(res.TempPath, targetRelPath, *item)
}

func (s *ProfileService) validateProfileSource(sourcePath string) error {
	absPath := sourcePath
	if !filepath.IsAbs(sourcePath) {
		absPath = s.cfg.GetProfileAbsPath(sourcePath)
	}

	fi, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return errors.New("目标配置文件不存在")
		}
		return fmt.Errorf("无法读取文件: %w", err)
	}
	if fi.Size() == 0 {
		return errors.New("文件内容为空")
	}
	if fi.Size() > domain.MaxProfileBytes {
		return errors.New("文件超出大小限制")
	}

	content, err := os.ReadFile(absPath)
	if err != nil {
		return fmt.Errorf("读取配置内容失败: %w", err)
	}

	if _, err := core.ComposeRuntimeYAML(s.cfg.GetConfig(), content); err != nil {
		return fmt.Errorf("配置语法错误: %w", err)
	}
	return nil
}
