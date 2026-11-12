package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mihomo-tray/internal/config"
	"mihomo-tray/internal/core"
	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/state"
)

var (
	ErrProfileUpdating = errors.New("订阅正在更新中，请稍候")
	ErrEmptyProfile    = errors.New("配置文件内容为空")
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

	p, ok := h.cfg.GetProfileByPath(activePath)
	if !ok || p.URL == "" {
		return false
	}

	if !h.st.TryAcquireProfileLock(activePath) {
		return false
	}
	defer h.st.ReleaseProfileLock(activePath)

	slog.Info("活跃配置文件丢失，尝试远程拉取恢复", "path", activePath)
	fCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if err := h.fetchAndCommit(fCtx, activePath, p.URL, &p); err != nil {
		slog.Debug("远程拉取恢复失败", "path", activePath, "err", err)
		return false
	}

	slog.Info("活跃配置已拉取恢复", "path", activePath)
	return true
}

func (h *ProfileHub) ImportLocal(sourcePath string) (targetRelPath string, isFirst bool, err error) {
	if sourcePath == "" {
		return "", false, nil
	}

	if err := h.ValidateSource(sourcePath); err != nil {
		return "", false, err
	}

	targetRelPath, _, err = h.cfg.SafeCopyUntrustedConfig(sourcePath)
	if err != nil {
		return "", false, err
	}

	h.cfg.RegisterNewProfile(targetRelPath)
	slog.Info("导入本地配置成功", "path", targetRelPath)
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
		return ErrProfileUpdating
	}
	defer h.st.ReleaseProfileLock(targetRelPath)

	p, ok := h.cfg.GetProfileByPath(targetRelPath)
	if !ok {
		return errors.New("订阅配置不存在")
	}
	if p.URL == "" {
		return errors.New("订阅链接为空")
	}

	if err := h.fetchAndCommit(ctx, targetRelPath, p.URL, &p); err != nil {
		return err
	}

	slog.Info("更新远程订阅成功", "path", targetRelPath)
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
		if !h.st.TryAcquireProfileLock(oldPath) {
			return false, ErrProfileUpdating
		}
		defer h.st.ReleaseProfileLock(oldPath)

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

func (h *ProfileHub) Delete(targetPath string) (wasActive bool, err error) {
	wasActive = (targetPath == h.cfg.GetActivePath())
	err = h.cfg.DeleteProfile(targetPath)
	return wasActive, err
}

func (h *ProfileHub) ValidateSource(sourcePath string) error {
	absPath, err := filepath.Abs(strings.TrimSpace(sourcePath))
	if err != nil {
		return errors.New("源文件路径无效")
	}

	fi, err := os.Stat(absPath)
	if err != nil {
		return err
	}
	if fi.Size() > domain.MaxProfileBytes {
		return fmt.Errorf("文件大小超出限制 (%d MB)", domain.MaxProfileBytes/(1024*1024))
	}

	content, err := os.ReadFile(absPath)
	if err != nil {
		return err
	}

	return h.validateBytes(content, false)
}

func (h *ProfileHub) fetchAndCommit(ctx context.Context, targetRelPath, url string, item *domain.ProfileItem) error {
	res, err := h.cfg.FetchRemoteProfile(ctx, url, h.getActiveProxyPort())
	if err != nil {
		return err
	}
	defer os.Remove(res.TempPath)

	tempBytes, err := os.ReadFile(res.TempPath)
	if err != nil {
		return err
	}

	isActive := (targetRelPath == h.cfg.GetActivePath())
	if err := h.validateBytes(tempBytes, isActive); err != nil {
		return err
	}

	item.Upload = res.Upload
	item.Download = res.Download
	item.Total = res.Total
	item.Expire = res.Expire
	item.LastUpdate = time.Now().Unix()

	return h.cfg.CommitRemoteProfile(res.TempPath, targetRelPath, *item)
}

func (h *ProfileHub) validateBytes(content []byte, isActive bool) error {
	if len(content) == 0 {
		return ErrEmptyProfile
	}
	if len(content) > domain.MaxProfileBytes {
		return fmt.Errorf("文件大小超出限制 (%d MB)", domain.MaxProfileBytes/(1024*1024))
	}

	var err error
	if isActive {
		_, err = core.ValidateRuntimeYAML(h.cfg.GetConfig(), content, h.cfg.BaseDir())
	} else {
		_, err = core.ComposeRuntimeYAML(h.cfg.GetConfig(), content)
	}
	if err != nil {
		return err
	}
	return nil
}
