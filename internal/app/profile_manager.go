package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
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

	if isManual && s.cfg.GetActivePath() == targetRelPath {
		if !s.st.TryBeginAction(state.ActionReload) {
			return errors.New("系统繁忙，请稍后重试")
		}
		defer s.st.EndAction()

		return s.deployer.DeployAndApply(ctx, targetRelPath, "应用手动订阅更新")
	}

	slog.Info("订阅更新成功", "path", targetRelPath, "manual", isManual)
	return nil
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
			return fmt.Errorf("订阅校验失败: %w", err)
		}
	} else {
		if _, err := core.ComposeRuntimeYAML(s.cfg.GetConfig(), tempBytes); err != nil {
			return fmt.Errorf("订阅语法错误: %w", err)
		}
	}

	item.Upload = res.Upload
	item.Download = res.Download
	item.Total = res.Total
	item.Expire = res.Expire
	item.LastUpdate = time.Now().Unix()

	return s.cfg.CommitRemoteProfile(res.TempPath, targetRelPath, *item)
}
