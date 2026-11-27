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

const maxConcurrentProfileUpdates = 3

var (
	ErrProfileUpdating = errors.New("订阅正在更新中，请稍候")
	errProfileTooLarge = fmt.Errorf("文件大小超出限制 (%d MB)", domain.MaxProfileBytes/(1024*1024))
)

type ProfileUpdateFailure struct {
	Path string
	Err  error
}

type BatchUpdateResult struct {
	Total         int
	Succeeded     int
	Skipped       int
	ActiveUpdated string
	Failures      []ProfileUpdateFailure
}

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

func normalizeAutoUpdate(autoUpdate bool, interval int) (bool, int) {
	if interval < 1 {
		return false, domain.DefaultUpdateInterval
	}
	if interval > domain.MaxUpdateInterval {
		interval = domain.MaxUpdateInterval
	}
	return autoUpdate, interval
}

func logProfileFetchSuccess(msg, path string, usedDirectFallback bool) {
	if usedDirectFallback {
		slog.Info(msg, "path", path, "via", "direct")
		return
	}
	slog.Info(msg, "path", path)
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
	if !ok || !p.IsRemote() {
		return valErr
	}

	slog.Info("活跃配置文件丢失，尝试远程拉取恢复", "path", activePath)
	fCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	usedDirect, err := h.fetchAndCommit(fCtx, activePath, p.URL, &p, true)
	if err != nil {
		return fmt.Errorf("远程拉取恢复失败: %w", err)
	}

	logProfileFetchSuccess("活跃配置已拉取恢复", activePath, usedDirect)
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

func (h *ProfileHub) AddRemote(ctx context.Context, rawName, url string, autoUpdate bool, interval int) (targetRelPath string, err error) {
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

	autoUpdate, interval = normalizeAutoUpdate(autoUpdate, interval)

	newItem := domain.ProfileItem{
		Name:       safeName,
		Path:       targetRelPath,
		URL:        cleanURL,
		AutoUpdate: autoUpdate,
		Interval:   interval,
	}

	usedDirect, err := h.fetchAndCommit(ctx, targetRelPath, newItem.URL, &newItem, false)
	if err != nil {
		return "", err
	}

	logProfileFetchSuccess("添加远程订阅成功", targetRelPath, usedDirect)
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
	if !p.IsRemote() {
		return errors.New("订阅链接为空")
	}

	usedDirect, err := h.fetchAndCommit(ctx, targetRelPath, p.URL, &p, true)
	if err != nil {
		return err
	}

	logProfileFetchSuccess("更新远程订阅成功", targetRelPath, usedDirect)
	return nil
}

func (h *ProfileHub) UpdateAllRemote(ctx context.Context, onEachSuccess func()) BatchUpdateResult {
	var targets []string
	for _, p := range h.cfg.GetProfiles() {
		if p.IsRemote() {
			targets = append(targets, p.Path)
		}
	}

	res := BatchUpdateResult{Total: len(targets)}
	if len(targets) == 0 {
		return res
	}

	sem := make(chan struct{}, maxConcurrentProfileUpdates)
	var wg sync.WaitGroup
	var mu sync.Mutex

dispatchLoop:
	for _, targetPath := range targets {
		select {
		case <-ctx.Done():
			break dispatchLoop
		case sem <- struct{}{}:
		}

		wg.Add(1)
		go func(relPath string) {
			defer wg.Done()
			defer func() { <-sem }()
			defer func() {
				if r := recover(); r != nil {
					panicErr := fmt.Errorf("panic: %v", r)
					mu.Lock()
					res.Failures = append(res.Failures, ProfileUpdateFailure{Path: relPath, Err: panicErr})
					mu.Unlock()
				}
			}()

			err := h.UpdateRemote(ctx, relPath)
			if err == nil {
				mu.Lock()
				res.Succeeded++
				if h.cfg.GetActivePath() == relPath {
					res.ActiveUpdated = relPath
				}
				mu.Unlock()

				if onEachSuccess != nil {
					onEachSuccess()
				}
				return
			}

			if errors.Is(err, context.Canceled) || ctx.Err() != nil {
				return
			}

			mu.Lock()
			if errors.Is(err, ErrProfileUpdating) || errors.Is(err, config.ErrProfileNotFound) {
				res.Skipped++
			} else {
				res.Failures = append(res.Failures, ProfileUpdateFailure{Path: relPath, Err: err})
			}
			mu.Unlock()
		}(targetPath)
	}

	wg.Wait()
	return res
}

func (h *ProfileHub) EditRemote(ctx context.Context, oldPath, newName, newURL string, newAutoUpdate bool, newInterval int) (urlChanged bool, err error) {
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

	newAutoUpdate, newInterval = normalizeAutoUpdate(newAutoUpdate, newInterval)

	if newName == p.Name && cleanURL == p.URL && newAutoUpdate == p.AutoUpdate && newInterval == p.Interval {
		return false, nil
	}

	urlChanged = (cleanURL != p.URL)
	p.Name = newName
	p.URL = cleanURL
	p.AutoUpdate = newAutoUpdate
	p.Interval = newInterval

	if urlChanged {
		usedDirect, err := h.fetchAndCommit(ctx, p.Path, cleanURL, &p, true)
		if err != nil {
			return false, err
		}
		logProfileFetchSuccess("更新订阅地址成功", p.Path, usedDirect)
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

func (h *ProfileHub) fetchAndCommit(ctx context.Context, targetRelPath, url string, item *domain.ProfileItem, requireExists bool) (bool, error) {
	res, err := h.cfg.FetchRemoteProfile(ctx, targetRelPath, url, h.getActiveProxyPort())
	if err != nil {
		return false, err
	}
	defer os.Remove(res.TempPath)

	tempBytes, err := os.ReadFile(res.TempPath)
	if err != nil {
		return false, err
	}

	if err := h.validateBytes(tempBytes); err != nil {
		return false, err
	}

	item.Upload = res.Upload
	item.Download = res.Download
	item.Total = res.Total
	item.Expire = res.Expire
	item.LastUpdate = time.Now().Unix()

	if err := h.cfg.CommitRemoteProfile(res.TempPath, targetRelPath, *item, requireExists); err != nil {
		return false, err
	}
	return res.UsedDirectFallback, nil
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
