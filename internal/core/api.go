package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/Microsoft/go-winio"

	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/state"
)

const (
	MaxAPIResponseSize = 5 * 1024 * 1024
	pipeDialTimeout    = 2 * time.Second
	pollReadyInterval  = 200 * time.Millisecond
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
	apiKeyPath      = "path"
)

type APIClient struct {
	st         *state.RuntimeState
	httpClient *http.Client
}

func NewAPIClient(st *state.RuntimeState) *APIClient {
	return &APIClient{
		st: st,
		httpClient: &http.Client{
			Transport: &http.Transport{
				Proxy: nil,
				DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
					timeout := pipeDialTimeout
					return winio.DialPipe(domain.IPCNamedPipe, &timeout)
				},
				MaxIdleConns:          100,
				MaxIdleConnsPerHost:   100,
				IdleConnTimeout:       90 * time.Second,
				ResponseHeaderTimeout: 12 * time.Second,
			},
		},
	}
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

func (c *APIClient) SyncAllRuntime(ctx context.Context, mode string, allowLan bool, tunEnable bool, tunDevice string) error {
	payload := map[string]any{
		apiKeyTun:      buildTunPayload(tunEnable, tunDevice),
		apiKeyMode:     mode,
		apiKeyAllowLan: allowLan,
	}
	return c.SyncConfigToKernel(ctx, payload)
}

func (c *APIClient) ForceReloadKernel(ctx context.Context, runtimeAbs string) error {
	if c.st.IsExiting() {
		return context.Canceled
	}
	payload := map[string]any{apiKeyPath: filepath.ToSlash(runtimeAbs)}
	_, err := c.DoRequest(ctx, http.MethodPut, "/configs?force=true", payload)
	return err
}

func (c *APIClient) RestartKernel(ctx context.Context) error {
	if c.st.IsExiting() {
		return context.Canceled
	}
	_, err := c.DoRequest(ctx, http.MethodPost, "/restart", nil)
	if err == nil {
		c.httpClient.CloseIdleConnections()
	}
	return err
}

func (c *APIClient) WaitForReady(ctx context.Context) error {
	ticker := time.NewTicker(pollReadyInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if c.st.IsExiting() {
				return context.Canceled
			}
			reqCtx, cancel := context.WithTimeout(ctx, pollReadyInterval)
			_, err := c.DoRequest(reqCtx, "GET", "/version", nil)
			cancel()

			if err == nil {
				return nil
			}
		}
	}
}

func (c *APIClient) SyncConfigToKernel(ctx context.Context, payload map[string]any) error {
	if c.st.IsExiting() {
		return context.Canceled
	}
	_, err := c.DoRequest(ctx, http.MethodPatch, "/configs", payload)
	return err
}

func (c *APIClient) GetKernelStatus(ctx context.Context) (*domain.KernelStatus, error) {
	body, err := c.DoRequest(ctx, "GET", "/configs", nil)
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
	if c.st.IsExiting() {
		return nil, context.Canceled
	}

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

	if !(method == http.MethodGet && (path == "/configs" || path == "/version")) {
		slog.Debug("发送内核控制指令", "method", method, "path", path)
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
		errMsg := strings.TrimSpace(string(body))
		if errMsg != "" {
			return nil, fmt.Errorf("[%d] %s", resp.StatusCode, errMsg)
		}
		return nil, fmt.Errorf("http status %d", resp.StatusCode)
	}

	return body, nil
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
