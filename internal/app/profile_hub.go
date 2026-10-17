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
	cfg       *config.Manager
	st        *state.RuntimeState
	proxyPort func() string
}

func NewProfileHub(cfg *config.Manager, st *state.RuntimeState, proxyPort func() string) *ProfileHub {
	return &ProfileHub{
		cfg:       cfg,
		st:        st,
		proxyPort: proxyPort,
	}
}

func (h *ProfileHub) getActiveProxyPort() string {
	if h.proxyPort != nil {
		return h.proxyPort()
	}
	return ""
}

func (h *ProfileHub) EnsureActiveValid(ctx context.Context) bool {
	activePath := h.cfg.GetActivePath()
	if activePath == "" {
		return true
	}

	if err := h.cfg.ValidatePhysicalFile(activePath); err == nil {
		return true
	}

	if p, ok := h.cfg.GetProfileByPath(activePath); ok && p.URL != "" {
		slog.Info("当前活跃配置物理文件丢失，尝试拉取远程订阅恢复", "path", activePath)
		fCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		err := h.fetchAndCommit(fCtx, activePath, p.URL, &p)
		cancel()

		if err == nil {
			slog.Info("配置已成功拉取恢复")
			return true
		}
		slog.Warn("拉取恢复失败", "err", err)
	}

	return false
}

func (h *ProfileHub) ImportLocal(sourcePath string) (targetRelPath string, isFirst bool, err error) {
	if sourcePath == "" {
		return "", false, nil
	}
	slog.Info("导入本地配置", "path", sourcePath)

	if err := h.ValidateSource(sourcePath); err != nil {
		return "", false, err
	}

	targetRelPath, _, err = h.cfg.SafeCopyUntrustedConfig(sourcePath)
	if err != nil {
		return "", false, fmt.Errorf("保存配置失败: %w", err)
	}

	h.cfg.RegisterNewProfile(targetRelPath)
	return targetRelPath, len(h.cfg.GetProfiles()) == 1, nil
}

func (h *ProfileHub) AddRemote(ctx context.Context, rawName, url string, interval int) (targetRelPath string, isFirst bool, err error) {
	cleanURL := strings.TrimSpace(url)
	if cleanURL == "" {
		return "", false, errors.New("订阅链接为空")
	}

	safeName, targetRelPath, err := h.cfg.AllocateRemoteProfilePath(rawName)
	if err != nil {
		return "", false, err
	}

	newItem := domain.ProfileItem{
		Name:       safeName,
		Path:       targetRelPath,
		URL:        cleanURL,
		AutoUpdate: interval > 0,
		Interval:   interval,
	}

	if err := h.fetchAndCommit(ctx, targetRelPath, newItem.URL, &newItem); err != nil {
		return "", false, err
	}

	slog.Info("添加远程订阅成功", "path", targetRelPath)
	return targetRelPath, len(h.cfg.GetProfiles()) == 1, nil
}

func (h *ProfileHub) UpdateRemote(ctx context.Context, targetRelPath string) error {
	if !h.st.TryAcquireProfileLock(targetRelPath) {
		return nil
	}
	defer h.st.ReleaseProfileLock(targetRelPath)

	p, ok := h.cfg.GetProfileByPath(targetRelPath)
	if !ok || p.URL == "" {
		return errors.New("配置或订阅链接不存在")
	}

	if err := h.fetchAndCommit(ctx, targetRelPath, p.URL, &p); err != nil {
		return err
	}

	slog.Info("订阅更新已落盘", "path", targetRelPath)
	return nil
}

func (h *ProfileHub) EditRemote(ctx context.Context, oldPath, newName, newURL string, newInterval int) (urlChanged bool, err error) {
	p, ok := h.cfg.GetProfileByPath(oldPath)
	if !ok {
		return false, errors.New("配置不存在")
	}

	if newName == p.Name && newURL == p.URL && newInterval == p.Interval {
		return false, nil
	}

	urlChanged = (newURL != p.URL)
	if urlChanged {
		if err := h.fetchAndCommit(ctx, p.Path, newURL, &p); err != nil {
			return false, err
		}
	}

	p.Name = newName
	p.URL = newURL
	p.Interval = newInterval
	p.AutoUpdate = newInterval > 0
	h.cfg.UpsertProfile(p)
	return urlChanged, nil
}

func (h *ProfileHub) EditLocal(targetPath, newName string) error {
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

func (h *ProfileHub) Delete(targetPath string) (wasActive bool, err error) {
	wasActive = (targetPath == h.cfg.GetActivePath())
	err = h.cfg.DeleteProfile(targetPath)
	return wasActive, err
}

func (h *ProfileHub) GetActivePath() string                 { return h.cfg.GetActivePath() }
func (h *ProfileHub) SetActiveProfile(path string)          { h.cfg.SetActiveProfile(path) }
func (h *ProfileHub) ClearActiveProfile()                   { h.cfg.SetActiveProfile("") }
func (h *ProfileHub) IsActive(path string) bool             { return path != "" && path == h.cfg.GetActivePath() }
func (h *ProfileHub) ValidateTarget(path string) error      { return h.cfg.ValidatePhysicalFile(path) }
func (h *ProfileHub) GetProfileInfo(path string) (domain.ProfileItem, bool) { return h.cfg.GetProfileByPath(path) }
func (h *ProfileHub) MoveProfileUp(path string)             { h.cfg.MoveProfile(path, -1) }
func (h *ProfileHub) MoveProfileDown(path string)           { h.cfg.MoveProfile(path, 1) }

func (h *ProfileHub) SetProfileInterval(payload string) {
	parts := strings.SplitN(payload, "|", 2)
	if len(parts) != 2 {
		return
	}
	interval, err := strconv.Atoi(parts[1])
	if err != nil {
		return
	}
	if p, ok := h.cfg.GetProfileByPath(parts[0]); ok {
		p.Interval = interval
		p.AutoUpdate = interval > 0
		h.cfg.UpsertProfile(p)
	}
}

func (h *ProfileHub) fetchAndCommit(ctx context.Context, targetRelPath, url string, item *domain.ProfileItem) error {
	res, err := h.cfg.FetchRemoteProfile(ctx, url, h.getActiveProxyPort())
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

func (h *ProfileHub) ValidateSource(sourcePath string) error {
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
