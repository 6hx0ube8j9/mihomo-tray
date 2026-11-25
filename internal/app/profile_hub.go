package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"mihomo-tray/internal/config"
	"mihomo-tray/internal/core"
	"mihomo-tray/internal/domain"
)

var (
	ErrProfileUpdating = errors.New("订阅正在更新中，请稍候")
	errProfileTooLarge = fmt.Errorf("文件大小超出限制 (%d MB)", domain.MaxProfileBytes/(1024*1024))
)

type ProfileHub struct {
	cfg       *config.Manager
	proxyPort func() string
	locks     sync.Map
}

func NewProfileHub(cfg *config.Manager, proxyPort func() string) *ProfileHub {
	return &ProfileHub{
		cfg:       cfg,
		proxyPort: proxyPort,
	}
}

func (h *ProfileHub) tryAcquireLock(path string) bool {
	_, loaded := h.locks.LoadOrStore(path, struct{}{})
	return !loaded
}

func (h *ProfileHub) releaseLock(path string) {
	h.locks.Delete(path)
}

func (h *ProfileHub) getActiveProxyPort() string {
	if h.proxyPort != nil {
		return h.proxyPort()
	}
	return ""
}

func (h *ProfileHub) EnsureActiveValid(ctx context.Context) error {
	activePath := h.cfg.GetActivePath()
	if activePath == "" {
		return nil
	}

	valErr := h.cfg.ValidatePhysicalFile(activePath)
	if valErr == nil {
		return nil
	}

	if !h.tryAcquireLock(activePath) {
		return ErrProfileUpdating
	}
	defer h.releaseLock(activePath)

	p, ok := h.cfg.GetProfileByPath(activePath)
	if !ok || p.URL == "" {
		return valErr
	}

	slog.Info("活跃配置文件丢失，尝试远程拉取恢复", "path", activePath)
	fCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if err := h.fetchAndCommit(fCtx, activePath, p.URL, &p, true); err != nil {
		return fmt.Errorf("远程拉取恢复失败: %w", err)
	}

	slog.Info("活跃配置已拉取恢复", "path", activePath)
	return nil
}

func (h *ProfileHub) ImportLocal(sourcePath string) (targetRelPath string, err error) {
	if sourcePath == "" {
		return "", nil
	}

	if err := h.ValidateSource(sourcePath); err != nil {
		return "", err
	}

	targetRelPath, _, err = h.cfg.SafeCopyUntrustedConfig(sourcePath)
	if err != nil {
		return "", err
	}

	h.cfg.RegisterNewProfile(targetRelPath)
	slog.Info("导入本地配置成功", "path", targetRelPath)
	return targetRelPath, nil
}

func (h *ProfileHub) AddRemote(ctx context.Context, rawName, url string, interval int) (targetRelPath string, err error) {
	cleanURL := strings.TrimSpace(url)
	if cleanURL == "" {
		return "", errors.New("订阅链接为空")
	}

	safeName, targetRelPath, err := h.cfg.AllocateRemoteProfilePath(rawName)
	if err != nil {
		return "", err
	}

	if !h.tryAcquireLock(targetRelPath) {
		return "", ErrProfileUpdating
	}
	defer h.releaseLock(targetRelPath)

	newItem := domain.ProfileItem{
		Name:       safeName,
		Path:       targetRelPath,
		URL:        cleanURL,
		AutoUpdate: interval > 0,
		Interval:   interval,
	}

	if err := h.fetchAndCommit(ctx, targetRelPath, newItem.URL, &newItem, false); err != nil {
		return "", err
	}

	slog.Info("添加远程订阅成功", "path", targetRelPath)
	return targetRelPath, nil
}

func (h *ProfileHub) UpdateRemote(ctx context.Context, targetRelPath string) error {
	if !h.tryAcquireLock(targetRelPath) {
		return ErrProfileUpdating
	}
	defer h.releaseLock(targetRelPath)

	p, ok := h.cfg.GetProfileByPath(targetRelPath)
	if !ok {
		return config.ErrProfileNotFound
	}
	if p.URL == "" {
		return errors.New("订阅链接为空")
	}

	if err := h.fetchAndCommit(ctx, targetRelPath, p.URL, &p, true); err != nil {
		return err
	}

	slog.Info("更新远程订阅成功", "path", targetRelPath)
	return nil
}

func (h *ProfileHub) EditRemote(ctx context.Context, oldPath, newName, newURL string, newInterval int) (urlChanged bool, err error) {
	cleanURL := strings.TrimSpace(newURL)
	if cleanURL == "" {
		return false, errors.New("订阅链接为空")
	}

	if !h.tryAcquireLock(oldPath) {
		return false, ErrProfileUpdating
	}
	defer h.releaseLock(oldPath)

	p, ok := h.cfg.GetProfileByPath(oldPath)
	if !ok {
		return false, config.ErrProfileNotFound
	}

	if newName == p.Name && cleanURL == p.URL && newInterval == p.Interval {
		return false, nil
	}

	urlChanged = (cleanURL != p.URL)
	p.Name = newName
	p.URL = cleanURL
	p.Interval = newInterval
	p.AutoUpdate = newInterval > 0

	if urlChanged {
		if err := h.fetchAndCommit(ctx, p.Path, cleanURL, &p, true); err != nil {
			return false, err
		}
		return true, nil
	}

	h.cfg.UpsertProfile(p)
	return false, nil
}

func (h *ProfileHub) Delete(targetPath string) (wasActive bool, err error) {
	if targetPath == "" {
		return false, nil
	}
	if !h.tryAcquireLock(targetPath) {
		return false, ErrProfileUpdating
	}
	defer h.releaseLock(targetPath)

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
		return errProfileTooLarge
	}

	content, err := os.ReadFile(absPath)
	if err != nil {
		return err
	}

	return h.validateBytes(content)
}

func (h *ProfileHub) fetchAndCommit(ctx context.Context, targetRelPath, url string, item *domain.ProfileItem, requireExists bool) error {
	res, err := h.cfg.FetchRemoteProfile(ctx, url, h.getActiveProxyPort())
	if err != nil {
		return err
	}
	defer os.Remove(res.TempPath)

	tempBytes, err := os.ReadFile(res.TempPath)
	if err != nil {
		return err
	}

	if err := h.validateBytes(tempBytes); err != nil {
		return err
	}

	item.Upload = res.Upload
	item.Download = res.Download
	item.Total = res.Total
	item.Expire = res.Expire
	item.LastUpdate = time.Now().Unix()

	return h.cfg.CommitRemoteProfile(res.TempPath, targetRelPath, *item, requireExists)
}

func (h *ProfileHub) validateBytes(content []byte) error {
	if len(content) == 0 {
		return config.ErrProfileEmpty
	}
	if len(content) > domain.MaxProfileBytes {
		return errProfileTooLarge
	}

	return core.ValidateProfileStatic(content)
}
