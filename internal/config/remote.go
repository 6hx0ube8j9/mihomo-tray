package config

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
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

const (
	RemoteFetchTimeout       = 20 * time.Second
	proxyFetchTimeout        = 8 * time.Second
	proxyTLSHandshakeTimeout = 5 * time.Second
	remoteRetryInterval      = 1 * time.Second
	proxyMaxAttempts         = 2
	directMaxAttempts        = 3
	directFallbackAttempts   = 2
)

var ErrRemoteHTML = errors.New("订阅返回内容为 HTML 网页")

type RemoteStatusError struct {
	StatusCode int
}

func (e *RemoteStatusError) Error() string {
	return fmt.Sprintf("订阅服务器返回 HTTP %d", e.StatusCode)
}

type nonFallbackError struct {
	err error
}

func (e *nonFallbackError) Error() string { return e.err.Error() }
func (e *nonFallbackError) Unwrap() error { return e.err }

type fetchOptions struct {
	proxyFunc     func(*http.Request) (*url.URL, error)
	timeout       time.Duration
	tlsTimeout    time.Duration
	maxAttempts   int
	stopOnTimeout bool
}

func sanitizeURL(rawURL string) string {
	if debugLogFullURL {
		return rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "[无效URL]"
	}
	return u.Scheme + "://" + u.Host
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

func isTimeoutError(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func shouldFallbackToDirect(ctx context.Context, err error) bool {
	if err == nil || ctx.Err() != nil {
		return false
	}

	var nfErr *nonFallbackError
	if errors.As(err, &nfErr) {
		return false
	}

	var statusErr *RemoteStatusError
	if errors.As(err, &statusErr) {
		return statusErr.StatusCode >= 500 ||
			statusErr.StatusCode == http.StatusForbidden ||
			statusErr.StatusCode == http.StatusProxyAuthRequired
	}

	return true
}

func (m *Manager) FetchRemoteProfile(ctx context.Context, subURL string, proxyPort string) (*domain.FetchResult, error) {
	subURL = strings.TrimSpace(subURL)
	proxyPort = strings.TrimSpace(proxyPort)

	if proxyPort != "" {
		if proxyURL, err := url.Parse("http://127.0.0.1:" + proxyPort); err == nil {
			res, proxyErr := m.fetchWithOptions(ctx, subURL, fetchOptions{
				proxyFunc:     http.ProxyURL(proxyURL),
				timeout:       proxyFetchTimeout,
				tlsTimeout:    proxyTLSHandshakeTimeout,
				maxAttempts:   proxyMaxAttempts,
				stopOnTimeout: true,
			})
			if proxyErr == nil {
				return res, nil
			}
			if !shouldFallbackToDirect(ctx, proxyErr) {
				return nil, proxyErr
			}

			slog.Warn("代理拉取订阅失败，尝试直连", "url", sanitizeURL(subURL), "err", proxyErr)

			directRes, directErr := m.fetchWithOptions(ctx, subURL, fetchOptions{
				proxyFunc:   nil,
				timeout:     RemoteFetchTimeout,
				maxAttempts: directFallbackAttempts,
			})
			if directErr == nil {
				slog.Info("订阅已通过直连拉取成功", "url", sanitizeURL(subURL))
				return directRes, nil
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}

			return nil, fmt.Errorf("代理与直连拉取均失败 (代理: %v): %w", proxyErr, directErr)
		}
		slog.Warn("代理端口无效，改用直连", "port", proxyPort)
	}

	return m.fetchWithOptions(ctx, subURL, fetchOptions{
		proxyFunc:   http.ProxyFromEnvironment,
		timeout:     RemoteFetchTimeout,
		maxAttempts: directMaxAttempts,
	})
}

func (m *Manager) fetchWithOptions(ctx context.Context, subURL string, opts fetchOptions) (*domain.FetchResult, error) {
	if opts.maxAttempts < 1 {
		opts.maxAttempts = 1
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	transport.Proxy = opts.proxyFunc
	if opts.tlsTimeout > 0 {
		transport.TLSHandshakeTimeout = opts.tlsTimeout
	}

	client := &http.Client{
		Timeout:   opts.timeout,
		Transport: transport,
	}

	var resp *http.Response
	var reqErr error
	attempts := 0

	for i := 0; i < opts.maxAttempts; i++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		attempts = i + 1

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, subURL, nil)
		if err != nil {
			return nil, &nonFallbackError{err: fmt.Errorf("无效的订阅链接: %w", sanitizeNetError(err))}
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

		if opts.stopOnTimeout && reqErr != nil && isTimeoutError(reqErr) {
			if resp != nil && resp.Body != nil {
				_ = resp.Body.Close()
			}
			break
		}

		if i < opts.maxAttempts-1 {
			if resp != nil && resp.Body != nil {
				_ = resp.Body.Close()
			}

			timer := time.NewTimer(remoteRetryInterval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
	}

	if reqErr != nil {
		return nil, fmt.Errorf("请求订阅失败 (尝试%d次): %w", attempts, sanitizeNetError(reqErr))
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
		return nil, &nonFallbackError{err: fmt.Errorf("保存临时文件失败: %w", err)}
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
