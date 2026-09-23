package config

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"mihomo-tray/internal/domain"
)

const RemoteFetchTimeout = 90 * time.Second

func (m *Manager) UpgradeSubscription(ctx context.Context, relPath string, proxyPort string, validator func(tmpPath string) error) (bool, error) {
	item, ok := m.GetProfileByPath(relPath)
	if !ok || item.URL == "" {
		return false, fmt.Errorf("配置文件不存在或 URL 为空")
	}

	fetchRes, err := m.FetchRemoteProfile(ctx, item.URL, proxyPort)
	if err != nil {
		return false, fmt.Errorf("拉取订阅失败: %w", err)
	}

	defer func() {
		if _, err := os.Stat(fetchRes.TempPath); err == nil {
			_ = os.Remove(fetchRes.TempPath)
		}
	}()

	if err := validator(fetchRes.TempPath); err != nil {
		return false, err
	}

	item.Upload = fetchRes.Upload
	item.Download = fetchRes.Download
	item.Total = fetchRes.Total
	item.Expire = fetchRes.Expire
	item.LastUpdate = time.Now().Unix()

	if err := m.CommitRemoteProfile(fetchRes.TempPath, relPath, item); err != nil {
		return false, fmt.Errorf("保存订阅失败: %w", err)
	}

	return true, nil
}

func (m *Manager) FetchRemoteProfile(ctx context.Context, subURL string, proxyPort string) (*domain.FetchResult, error) {
	subURL = strings.TrimSpace(subURL)
	slog.Info("开始拉取订阅", "url", subURL)

	transport := &http.Transport{Proxy: http.ProxyFromEnvironment}

	if proxyPort != "" {
		if u, err := url.Parse("http://127.0.0.1:" + proxyPort); err == nil {
			transport.Proxy = http.ProxyURL(u)
		}
	}

	client := &http.Client{
		Timeout:   RemoteFetchTimeout,
		Transport: transport,
	}

	var resp *http.Response
	var reqErr error
	maxRetries := 3

	for i := 0; i < maxRetries; i++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		req, err := http.NewRequestWithContext(ctx, "GET", subURL, nil)
		if err != nil {
			return nil, err
		}

		req.Header.Set("User-Agent", domain.DefaultUserAgent)
		req.Header.Set("Accept", "application/yaml, text/yaml, text/plain, */*")
		req.Header.Set("Connection", "keep-alive")

		resp, reqErr = client.Do(req)

		if reqErr == nil && resp.StatusCode < 500 {
			break
		}

		if i < maxRetries-1 {
			if resp != nil && resp.Body != nil {
				_ = resp.Body.Close()
			}
			slog.Warn("网络请求异常，准备重试", "url", subURL, "retry", i+1, "err", reqErr)
			time.Sleep(1500 * time.Millisecond)
		}
	}

	if reqErr != nil {
		return nil, fmt.Errorf("网络请求失败 (已重试%d次): %w", maxRetries, reqErr)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("服务器响应异常，状态码: %d", resp.StatusCode)
	}

	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.Contains(contentType, "text/html") {
		return nil, fmt.Errorf("拉取异常: 目标服务器返回了 HTML 页面，可能已被防火墙拦截")
	}

	profilesDirAbs := filepath.Join(m.baseDir, ProfilesDir)
	_ = os.MkdirAll(profilesDirAbs, 0755)

	tmpFile, err := os.CreateTemp(profilesDirAbs, "sub_*.tmp")
	if err != nil {
		return nil, err
	}
	tmpName := tmpFile.Name()

	limitReader := io.LimitReader(resp.Body, domain.MaxProfileBytes)
	_, copyErr := io.Copy(tmpFile, limitReader)

	tmpFile.Close()

	if copyErr != nil {
		_ = os.Remove(tmpName)
		return nil, copyErr
	}

	res := &domain.FetchResult{TempPath: tmpName}

	if userInfo := resp.Header.Get("subscription-userinfo"); userInfo != "" {
		parts := strings.Split(userInfo, ";")
		for _, part := range parts {
			kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
			if len(kv) == 2 {
				val, _ := strconv.ParseInt(kv[1], 10, 64)
				switch strings.ToLower(kv[0]) {
				case "upload":
					res.Upload = val
				case "download":
					res.Download = val
				case "total":
					res.Total = val
				case "expire":
					res.Expire = val
				}
			}
		}
	}

	return res, nil
}

func (m *Manager) CommitRemoteProfile(tempPath string, targetRelPath string, item domain.ProfileItem) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	targetAbs := filepath.Join(m.baseDir, filepath.FromSlash(targetRelPath))

	if err := os.Rename(tempPath, targetAbs); err != nil {
		return err
	}

	found := false
	for i, p := range m.data.Profiles.Items {
		if p.Path == targetRelPath {
			m.data.Profiles.Items[i] = item
			found = true
			break
		}
	}

	if !found {
		m.data.Profiles.Items = append(m.data.Profiles.Items, item)
		if len(m.data.Profiles.Items) > domain.MaxProfileCount {
			m.data.Profiles.Items = append(m.data.Profiles.Items[:1], m.data.Profiles.Items[2:]...)
		}
	}

	m.lockedSave()
	return nil
}

func (m *Manager) GetProfileByPath(relPath string) (domain.ProfileItem, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	for _, p := range m.data.Profiles.Items {
		if p.Path == relPath {
			return p, true
		}
	}
	return domain.ProfileItem{}, false
}
