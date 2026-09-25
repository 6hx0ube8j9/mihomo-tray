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
	"strings"
	"time"

	"github.com/Microsoft/go-winio"

	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/state"
)

const MaxAPIResponseSize = 5 * 1024 * 1024

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
					timeout := 2 * time.Second
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

func (c *APIClient) DoRequest(ctx context.Context, method, path string, payload interface{}) ([]byte, error) {
	if c.st.IsExiting() {
		return nil, context.Canceled
	}

	url := "http://localhost/" + strings.TrimPrefix(path, "/")

	var bodyReader io.Reader
	if payload != nil {
		byteData, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		bodyReader = bytes.NewReader(byteData)
	} else if method == http.MethodPut || method == http.MethodPost || method == http.MethodPatch {
		bodyReader = strings.NewReader("{}")
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, err
	}

	if bodyReader != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	if !(method == http.MethodGet && path == "/configs") {
		slog.Debug("发送内核 IPC 管道请求", "method", method, "path", path)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent || resp.ContentLength == 0 {
		_, _ = io.Copy(io.Discard, resp.Body)
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return nil, nil
		}
		return nil, fmt.Errorf("API Status Error: %d", resp.StatusCode)
	}

	limitReader := io.LimitReader(resp.Body, MaxAPIResponseSize)
	body, err := io.ReadAll(limitReader)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errMsg := strings.TrimSpace(string(body))
		return nil, fmt.Errorf("API Error %d: %s", resp.StatusCode, errMsg)
	}

	return body, nil
}

func (c *APIClient) ForceReloadKernel(ctx context.Context, payload map[string]interface{}) error {
	if c.st.IsExiting() {
		return context.Canceled
	}
	_, err := c.DoRequest(ctx, http.MethodPut, "/configs?force=true", payload)
	return err
}

func (c *APIClient) SyncConfigToKernel(ctx context.Context, payload map[string]interface{}) error {
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
		return nil, fmt.Errorf("解析内核状态失败: %w", err)
	}
	return &status, nil
}
