package config

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/fs"
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

	tmpName, err := fs.SaveTempWithLimit(m.ProfilesDirAbs(), "sub_*.tmp", resp.Body, domain.MaxProfileBytes)
	if err != nil {
		return nil, fmt.Errorf("订阅内容写入本地失败: %w", err)
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
	if _, exists := m.GetProfileByPath(item.Path); !exists {
		if err := m.CheckProfileLimit(); err != nil {
			return err
		}
	}

	targetAbs := m.GetProfileAbsPath(targetRelPath)
	if err := fs.ReplaceAtomic(tempPath, targetAbs); err != nil {
		return fmt.Errorf("配置落盘受阻，文件可能被系统占用。\n\n%w", err)
	}

	m.UpsertProfile(item)
	return nil
}
