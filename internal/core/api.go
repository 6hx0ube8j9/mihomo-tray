package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/Microsoft/go-winio"

	"mihomo-tray/internal/domain"
)

const (
	MaxAPIResponseSize = 5 * 1024 * 1024
	pipeDialTimeout    = 2 * time.Second
	defaultAPITimeout  = 10 * time.Second
)

const (
	apiKeyMixedPort = "mixed-port"
	apiKeySocksPort = "socks-port"
	apiKeyPort      = "port"
	apiKeyMode      = "mode"
	apiKeyAllowLan  = "allow-lan"
	apiKeyTun       = "tun"
	apiKeyTunEnable = "enable"
	apiKeyTunDevice = "device"
	apiKeyPayload   = "payload"
)

type APIClient struct {
	pipePath   string
	httpClient *http.Client
}

type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("[%d] %s", e.StatusCode, e.Message)
}

func NewAPIClient(pipePath string) *APIClient {
	return &APIClient{
		pipePath: pipePath,
		httpClient: &http.Client{
			Transport: &http.Transport{
				Proxy: nil,
				DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
					dialCtx, cancel := context.WithTimeout(ctx, pipeDialTimeout)
					defer cancel()
					return winio.DialPipeContext(dialCtx, pipePath)
				},
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 100,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
}

func withDefaultTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if _, hasDeadline := ctx.Deadline(); hasDeadline {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, d)
}

func (c *APIClient) CloseIdleConnections() {
	if c == nil || c.httpClient == nil {
		return
	}
	c.httpClient.CloseIdleConnections()
}

func (c *APIClient) Ping(ctx context.Context) error {
	reqCtx, cancel := withDefaultTimeout(ctx, defaultAPITimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, "http://localhost/version", nil)
	if err != nil {
		return err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return fmt.Errorf("http status %d", resp.StatusCode)
}

func (c *APIClient) UpdatePorts(ctx context.Context, mixed, socks, httpPort int) error {
	payload := map[string]any{
		apiKeyMixedPort: mixed,
		apiKeySocksPort: socks,
		apiKeyPort:      httpPort,
	}
	return c.SyncConfigToKernel(ctx, payload)
}

func (c *APIClient) UpdateTun(ctx context.Context, enable bool, device string) error {
	return c.SyncConfigToKernel(ctx, map[string]any{
		apiKeyTun: buildTunPayload(enable, device),
	})
}

func (c *APIClient) UpdateMode(ctx context.Context, mode string) error {
	return c.SyncConfigToKernel(ctx, map[string]any{apiKeyMode: mode})
}

func (c *APIClient) UpdateAllowLan(ctx context.Context, enable bool) error {
	return c.SyncConfigToKernel(ctx, map[string]any{apiKeyAllowLan: enable})
}

func (c *APIClient) ForceReloadKernel(ctx context.Context, yamlPayload string) error {
	payload := map[string]any{apiKeyPayload: yamlPayload}
	_, err := c.DoRequest(ctx, http.MethodPut, "/configs?force=true", payload)
	return err
}

func (c *APIClient) RestartKernel(ctx context.Context) error {
	reqCtx, cancel := withDefaultTimeout(ctx, defaultAPITimeout)
	defer cancel()

	_, err := c.DoRequest(reqCtx, http.MethodPost, "/restart", nil)
	if err == nil {
		c.httpClient.CloseIdleConnections()
	}
	return err
}

func (c *APIClient) SyncConfigToKernel(ctx context.Context, payload map[string]any) error {
	reqCtx, cancel := withDefaultTimeout(ctx, defaultAPITimeout)
	defer cancel()

	_, err := c.DoRequest(reqCtx, http.MethodPatch, "/configs", payload)
	return err
}

func (c *APIClient) GetKernelStatus(ctx context.Context) (*domain.KernelStatus, error) {
	reqCtx, cancel := withDefaultTimeout(ctx, defaultAPITimeout)
	defer cancel()

	body, err := c.DoRequest(reqCtx, http.MethodGet, "/configs", nil)
	if err != nil {
		return nil, err
	}

	var status domain.KernelStatus
	if err := json.Unmarshal(body, &status); err != nil {
		return nil, fmt.Errorf("json unmarshal: %w", err)
	}
	return &status, nil
}

func (c *APIClient) DoRequest(ctx context.Context, method, path string, payload any) ([]byte, error) {
	url := "http://localhost/" + strings.TrimPrefix(path, "/")

	var bodyReader io.Reader
	if payload != nil {
		byteData, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("json marshal: %w", err)
		}
		bodyReader = bytes.NewReader(byteData)
	} else if method == http.MethodPut || method == http.MethodPost || method == http.MethodPatch {
		bodyReader = strings.NewReader("{}")
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}

	if bodyReader != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ipc request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent || resp.ContentLength == 0 {
		_, _ = io.Copy(io.Discard, resp.Body)
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return nil, nil
		}
		return nil, fmt.Errorf("http status %d", resp.StatusCode)
	}

	limitReader := io.LimitReader(resp.Body, MaxAPIResponseSize)
	body, err := io.ReadAll(limitReader)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errMsg := parseAPIErrorMessage(body)
		if errMsg != "" {
			return nil, &APIError{StatusCode: resp.StatusCode, Message: errMsg}
		}
		return nil, fmt.Errorf("http status %d", resp.StatusCode)
	}

	return body, nil
}

func parseAPIErrorMessage(body []byte) string {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return ""
	}
	var payload struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &payload); err == nil && strings.TrimSpace(payload.Message) != "" {
		return strings.TrimSpace(payload.Message)
	}
	return trimmed
}

func buildTunPayload(enable bool, device string) map[string]any {
	payload := map[string]any{
		apiKeyTunEnable: enable,
	}
	if device != "" {
		payload[apiKeyTunDevice] = device
	}
	return payload
}
