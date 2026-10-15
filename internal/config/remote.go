package config

import (
	"context"
	"errors"
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

	transport := http.DefaultTransport.(*http.Transport).Clone()

	if proxyPort != "" {
		if u, err := url.Parse("http://127.0.0.1:" + proxyPort); err == nil {
			transport.Proxy = http.ProxyURL(u)
		} else {
			slog.Warn("代理端口解析失败，降级为系统环境变量代理", "port", proxyPort)
		}
	} else {
		transport.Proxy = http.ProxyFromEnvironment
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
			
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(1500 * time.Millisecond):
			}
		}
	}

	if reqErr != nil {
		return nil, fmt.Errorf("连接订阅服务器失败 (已重试%d次): %w", maxRetries, reqErr)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("服务器拒绝提供配置 (HTTP %d)", resp.StatusCode)
	}

	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.Contains(contentType, "text/html") {
		return nil, errors.New("订阅链接返回了网页内容而非代理配置")
	}

	tmpName, err := fs.SaveTempWithLimit(m.ProfilesDirAbs(), "sub_*.tmp", resp.Body, domain.MaxProfileBytes)
	if err != nil {
		return nil, fmt.Errorf("订阅写入临时文件失败: %w", err)
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
		return fmt.Errorf("保存配置文件失败: %w", err)
	}

	m.UpsertProfile(item)
	return nil
}
