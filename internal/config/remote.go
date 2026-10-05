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
			return nil, fmt.Errorf("无效的订阅链接格式: %w", err)
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
			slog.Warn("网络不稳定，准备进行自动重试", "url", subURL, "retry", i+1, "err", reqErr)
			time.Sleep(1500 * time.Millisecond)
		}
	}

	if reqErr != nil {
		return nil, fmt.Errorf("无法连接至订阅服务器 (已重试%d次)。请检查网络状态或代理设置。\n\n%w", maxRetries, reqErr)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("服务器拒绝提供配置，HTTP 状态码: %d", resp.StatusCode)
	}

	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.Contains(contentType, "text/html") {
		return nil, fmt.Errorf("目标链接无效或提供的内容非代理配置 (服务器返回了网页内容)")
	}

	profilesDirAbs := filepath.Join(m.baseDir, ProfilesDir)
	_ = os.MkdirAll(profilesDirAbs, 0755)

	tmpFile, err := os.CreateTemp(profilesDirAbs, "sub_*.tmp")
	if err != nil {
		return nil, fmt.Errorf("系统缓存文件创建失败，请检查磁盘权限: %w", err)
	}
	tmpName := tmpFile.Name()

	limitReader := io.LimitReader(resp.Body, domain.MaxProfileBytes)
	_, copyErr := io.Copy(tmpFile, limitReader)

	var extra [1]byte
	if n, _ := resp.Body.Read(extra[:]); n > 0 {
		_ = tmpFile.Close()
		_ = os.Remove(tmpName)
		return nil, fmt.Errorf("订阅文件体积超出上限 (最大允许 %d MB)", domain.MaxProfileBytes/(1024*1024))
	}

	if copyErr == nil {
		_ = tmpFile.Sync()
	}
	
	tmpFile.Close()

	if copyErr != nil {
		_ = os.Remove(tmpName)
		return nil, fmt.Errorf("订阅内容写入本地失败: %w", copyErr)
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
	targetAbs := filepath.Join(m.baseDir, filepath.FromSlash(targetRelPath))
	if err := os.Rename(tempPath, targetAbs); err != nil {
		return fmt.Errorf("配置落盘受阻，文件可能被系统占用。\n\n%w", err)
	}

	m.UpsertProfile(item)
	
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
