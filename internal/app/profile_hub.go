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

type ProfileHub struct {
    cfg         *config.Manager
    st          *state.RuntimeState
    isProxyLive func() bool
    guard       *ProxyGuard
}

func NewProfileHub(cfg *config.Manager, st *state.RuntimeState, isProxyLive func() bool, guard *ProxyGuard) *ProfileHub {
    return &ProfileHub{
        cfg:         cfg,
        st:          st,
        isProxyLive: isProxyLive,
        guard:       guard,
    }
}

func (h *ProfileHub) getActiveProxyPort() string {
    if h.isProxyLive != nil && h.isProxyLive() {
        return h.cfg.GetEffectiveLocalProxyPortStr()
    }
    return ""
}

func (h *ProfileHub) EnsureActiveValid(ctx context.Context) {
	activePath := h.cfg.GetActivePath()
	if activePath == "" {
		return
	}

	if err := h.cfg.ValidatePhysicalFile(activePath); err == nil {
		return
	}

	if p, ok := h.cfg.GetProfileByPath(activePath); ok && p.URL != "" {
		slog.Info("当前活跃配置物理文件丢失，尝试拉取远程订阅恢复", "path", activePath)
		fCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		err := h.fetchAndCommit(fCtx, activePath, p.URL, &p)
		cancel()

		if err == nil {
			slog.Info("配置已成功拉取恢复")
			return
		}
		slog.Warn("拉取恢复失败", "err", err)
	}

	h.FallbackToEmpty(ctx, "活跃配置物理文件无效且无法恢复")
}

func (h *ProfileHub) FallbackToEmpty(ctx context.Context, reason string) {
	slog.Warn("配置失效，系统安全回退至空配置", "reason", reason)

	h.cfg.Update(func(c *domain.TrayConfig) {
		c.Profiles.Active = ""
		bProxy := false
		c.General.SystemProxy = &bProxy
		c.Config.Tun.Enable = false
	})

	h.st.SetTunRequestedTime(time.Time{})
	h.st.SetTunAlive(false)

	if h.st.GetPhase() == domain.PhaseRunning {
		_ = h.deployer.DeployAndApply(ctx, "", "回退空配置")
	} else {
		cfg := h.cfg.GetConfig()
		_, _ = core.DeployRuntimeConfig(cfg, "", h.cfg.BaseDir())
	}

	h.guard.Sync()
}

func (h *ProfileHub) SwitchProfile(ctx context.Context, targetPath string) error {
	if targetPath == "" {
		h.FallbackToEmpty(ctx, "用户主动切换为空配置")
		return nil
	}

	if targetPath == h.cfg.GetActivePath() {
		return nil
	}

	if !h.st.TryBeginAction(state.ActionSwitchProfile) {
		return errors.New("系统繁忙，请稍后重试")
	}
	defer h.st.EndAction()

	if err := h.deployer.DeployAndApply(ctx, targetPath, "切换配置"); err != nil {
		return err
	}

	h.cfg.SetActiveProfile(targetPath)
	return nil
}

func (h *ProfileHub) ImportLocalProfile(ctx context.Context, sourcePath string) error {
	if sourcePath == "" {
		return nil
	}
	slog.Info("导入本地配置", "path", sourcePath)

	if err := h.validateSource(sourcePath); err != nil {
		return err
	}

	targetRelPath, _, err := h.cfg.SafeCopyUntrustedConfig(sourcePath)
	if err != nil {
		return fmt.Errorf("保存配置失败: %w", err)
	}

	h.cfg.RegisterNewProfile(targetRelPath)

	if len(h.cfg.GetProfiles()) == 1 {
		return h.SwitchProfile(ctx, targetRelPath)
	}
	return nil
}

func (h *ProfileHub) AddRemoteProfile(ctx context.Context, rawName, url string, interval int) error {
	cleanURL := strings.TrimSpace(url)
	if cleanURL == "" {
		return errors.New("订阅链接为空")
	}

	safeName, targetRelPath, err := h.cfg.AllocateRemoteProfilePath(rawName)
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

	if err := h.fetchAndCommit(ctx, targetRelPath, newItem.URL, &newItem); err != nil {
		return err
	}

	slog.Info("添加远程订阅成功", "path", targetRelPath)
	if len(h.cfg.GetProfiles()) == 1 {
		return h.SwitchProfile(ctx, targetRelPath)
	}
	return nil
}

func (h *ProfileHub) UpdateRemoteProfile(ctx context.Context, targetRelPath string, isManual bool) error {
	if !h.st.TryAcquireProfileLock(targetRelPath) {
		return nil
	}
	defer h.st.ReleaseProfileLock(targetRelPath)

	p, ok := h.cfg.GetProfileByPath(targetRelPath)
	if !ok || p.URL == "" {
		if isManual {
			return errors.New("配置或订阅链接不存在")
		}
		return nil
	}

	err := h.fetchAndCommit(ctx, targetRelPath, p.URL, &p)
	if err != nil {
		if isManual {
			return err
		}
		slog.Warn("自动更新订阅失败", "path", targetRelPath, "err", err)
		return nil
	}

	slog.Info("订阅更新已落盘", "path", targetRelPath, "manual", isManual)

	if isManual && h.cfg.GetActivePath() == targetRelPath {
		if !h.st.TryBeginAction(state.ActionReload) {
			return errors.New("系统繁忙，配置已保存，请稍后手动重载")
		}
		defer h.st.EndAction()
		return h.deployer.DeployAndApply(ctx, targetRelPath, "应用手动订阅更新")
	}

	return nil
}

func (h *ProfileHub) EditRemoteProfile(ctx context.Context, oldPath, newName, newURL string, newInterval int) error {
	p, ok := h.cfg.GetProfileByPath(oldPath)
	if !ok {
		return errors.New("配置不存在")
	}

	if newName == p.Name && newURL == p.URL && newInterval == p.Interval {
		return nil
	}

	urlChanged := newURL != p.URL
	if urlChanged {
		if err := h.fetchAndCommit(ctx, p.Path, newURL, &p); err != nil {
			return err
		}
	}

	p.Name = newName
	p.URL = newURL
	p.Interval = newInterval
	p.AutoUpdate = newInterval > 0
	h.cfg.UpsertProfile(p)

	if urlChanged && h.cfg.GetActivePath() == p.Path {
		if !h.st.TryBeginAction(state.ActionReload) {
			return errors.New("配置已保存，但系统繁忙未能立即热重载")
		}
		defer h.st.EndAction()
		return h.deployer.DeployAndApply(ctx, p.Path, "应用订阅地址变更")
	}
	return nil
}

func (h *ProfileHub) EditLocalProfile(ctx context.Context, targetPath, newName string) error {
	p, ok := h.cfg.GetProfileByPath(targetPath)
	if !ok {
		return errors.New("配置不存在")
	}
	if p.Name != newName {
		p.Name = newName
		h.cfg.UpsertProfile(p)
	}
	return nil
}

func (h *ProfileHub) DeleteProfile(ctx context.Context, targetPath string) error {
	isActive := targetPath == h.cfg.GetActivePath()

	if err := h.cfg.DeleteProfile(targetPath); err != nil {
		slog.Warn("清理本地物理配置失败", "path", targetPath, "err", err)
	}

	if isActive {
		slog.Info("活跃配置已被删除，执行降级回退")
		h.FallbackToEmpty(ctx, "正在使用的配置文件被删除")
	}
	return nil
}

func (h *ProfileHub) GetProfileInfo(path string) (domain.ProfileItem, bool) {
	return h.cfg.GetProfileByPath(path)
}

func (h *ProfileHub) MoveProfileUp(path string)   { h.cfg.MoveProfile(path, -1) }
func (h *ProfileHub) MoveProfileDown(path string) { h.cfg.MoveProfile(path, 1) }

func (h *ProfileHub) SetProfileInterval(payload string) {
	parts := strings.SplitN(payload, "|", 2)
	if len(parts) != 2 {
		return
	}
	path := parts[0]
	interval, err := strconv.Atoi(parts[1])
	if err != nil {
		return
	}

	if p, ok := h.cfg.GetProfileByPath(path); ok {
		p.Interval = interval
		p.AutoUpdate = interval > 0
		h.cfg.UpsertProfile(p)
	}
}

func (h *ProfileHub) fetchAndCommit(ctx context.Context, targetRelPath, url string, item *domain.ProfileItem) error {
	proxyPort := h.getActiveProxyPort()
	res, err := h.cfg.FetchRemoteProfile(ctx, url, proxyPort)
	if err != nil {
		return err
	}
	defer os.Remove(res.TempPath)

	tempBytes, err := os.ReadFile(res.TempPath)
	if err != nil || len(tempBytes) == 0 {
		return errors.New("订阅文件内容为空")
	}

	if targetRelPath == h.cfg.GetActivePath() {
		if _, err := core.ValidateRuntimeYAML(h.cfg.GetConfig(), tempBytes, h.cfg.BaseDir()); err != nil {
			return fmt.Errorf("订阅配置内容校验失败: %w", err)
		}
	} else {
		if _, err := core.ComposeRuntimeYAML(h.cfg.GetConfig(), tempBytes); err != nil {
			return fmt.Errorf("订阅文件语法错误: %w", err)
		}
	}

	item.Upload = res.Upload
	item.Download = res.Download
	item.Total = res.Total
	item.Expire = res.Expire
	item.LastUpdate = time.Now().Unix()

	return h.cfg.CommitRemoteProfile(res.TempPath, targetRelPath, *item)
}

func (h *ProfileHub) validateSource(sourcePath string) error {
	absPath := sourcePath
	if !filepath.IsAbs(sourcePath) {
		absPath = h.cfg.GetProfileAbsPath(sourcePath)
	}

	fileName := filepath.Base(sourcePath)
	fi, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%s: 目标配置文件不存在", fileName)
		}
		return fmt.Errorf("%s: 无法读取文件: %w", fileName, err)
	}
	if fi.Size() == 0 {
		return fmt.Errorf("%s: 文件内容为空", fileName)
	}
	if fi.Size() > domain.MaxProfileBytes {
		return fmt.Errorf("%s: 文件超出大小限制", fileName)
	}

	content, err := os.ReadFile(absPath)
	if err != nil {
		return fmt.Errorf("%s: 读取配置内容失败: %w", fileName, err)
	}

	if _, err := core.ComposeRuntimeYAML(h.cfg.GetConfig(), content); err != nil {
		return fmt.Errorf("%s: 配置语法错误: %w", fileName, err)
	}
	return nil
}
