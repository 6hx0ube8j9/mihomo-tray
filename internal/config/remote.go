package config

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/fs"
)

// debugLogFullURL controls whether to log unredacted subscription URLs with sensitive tokens for debugging.
const debugLogFullURL = false

const RemoteFetchTimeout = 90 * time.Second

var ErrRemoteHTML = errors.New("订阅返回内容为 HTML 网页")

type RemoteStatusError struct {
	StatusCode int
}

func (e *RemoteStatusError) Error() string {
	return fmt.Sprintf("订阅服务器返回 HTTP %d", e.StatusCode)
}

func sanitizeURL(rawURL string) string {
	if debugLogFullURL {
		return rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return "[无效URL]"
	}
	u.RawQuery = ""
	u.User = nil
	return u.String()
}

func sanitizeNetError(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		if urlErr.Err != nil {
			return fmt.Errorf("%s %s: %w", urlErr.Op, sanitizeURL(urlErr.URL), urlErr.Err)
		}
		return fmt.Errorf("%s %s", urlErr.Op, sanitizeURL(urlErr.URL))
	}
	return err
}

func (m *Manager) FetchRemoteProfile(ctx context.Context, subURL string, proxyPort string) (*domain.FetchResult, error) {
	subURL = strings.TrimSpace(subURL)

	transport := http.DefaultTransport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()

	if proxyPort != "" {
		if u, err := url.Parse("http://127.0.0.1:" + proxyPort); err == nil {
			transport.Proxy = http.ProxyURL(u)
		} else {
			slog.Warn("代理端口无效，使用系统代理", "port", proxyPort)
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
	const maxRetries = 3

	for i := 0; i < maxRetries; i++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		req, err := http.NewRequestWithContext(ctx, "GET", subURL, nil)
		if err != nil {
			return nil, fmt.Errorf("无效的订阅链接: %w", sanitizeNetError(err))
		}

		req.Header.Set("User-Agent", domain.DefaultUserAgent)
		req.Header.Set("Accept", "application/yaml, text/yaml, text/plain, */*")
		req.Header.Set("Connection", "keep-alive")

		resp, reqErr = client.Do(req)
		if ctx.Err() != nil {
			if resp != nil && resp.Body != nil {
				_ = resp.Body.Close()
			}
			return nil, ctx.Err()
		}
		if reqErr == nil && resp.StatusCode < 500 {
			break
		}

		if i < maxRetries-1 {
			if resp != nil && resp.Body != nil {
				_ = resp.Body.Close()
			}

			timer := time.NewTimer(1500 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
	}

	if reqErr != nil {
		return nil, fmt.Errorf("请求订阅失败 (%d次重试): %w", maxRetries, sanitizeNetError(reqErr))
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, &RemoteStatusError{StatusCode: resp.StatusCode}
	}

	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.Contains(contentType, "text/html") {
		return nil, ErrRemoteHTML
	}

	tmpName, err := fs.SaveTempWithLimit(m.ProfilesDirAbs(), "sub_*.tmp", resp.Body, domain.MaxProfileBytes)
	if err != nil {
		return nil, fmt.Errorf("保存临时文件失败: %w", err)
	}

	res := &domain.FetchResult{TempPath: tmpName}

	if userInfo := resp.Header.Get("subscription-userinfo"); userInfo != "" {
		for _, part := range strings.Split(userInfo, ";") {
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

func (m *Manager) CommitRemoteProfile(tempPath string, targetRelPath string, item domain.ProfileItem, requireExists bool) error {
	_, exists := m.GetProfileByPath(item.Path)
	if requireExists {
		if !exists {
			return ErrProfileNotFound
		}
	} else {
		if exists {
			return ErrProfileNameConflict
		}
		if err := m.CheckProfileLimit(); err != nil {
			return err
		}
	}

	targetAbs := m.GetProfileAbsPath(targetRelPath)
	if err := fs.ReplaceAtomic(tempPath, targetAbs); err != nil {
		return fmt.Errorf("更新配置失败: %w", err)
	}

	var commitErr error
	m.TryUpdate(func(cfg *domain.TrayConfig) bool {
		if requireExists {
			for i, p := range cfg.Profiles.Items {
				if p.Path == item.Path {
					cfg.Profiles.Items[i] = item
					return true
				}
			}
			commitErr = ErrProfileNotFound
			return false
		}

		for _, p := range cfg.Profiles.Items {
			if p.Path == item.Path {
				commitErr = ErrProfileNameConflict
				return false
			}
		}
		if len(cfg.Profiles.Items) >= domain.MaxProfileCount {
			commitErr = ErrProfileLimitExceeded
			return false
		}
		cfg.Profiles.Items = append(cfg.Profiles.Items, item)
		return true
	})

	if commitErr != nil {
		_ = os.Remove(targetAbs)
		return commitErr
	}

	return nil
}
