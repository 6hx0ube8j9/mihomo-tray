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
	"sync"
	"time"
	"log/slog"

	"mihomo-tray/internal/config"
	"mihomo-tray/internal/state"
)

type APIClient struct {
	cfg        *config.Manager
	st         *state.RuntimeState
	httpClient *http.Client

	connMu  sync.RWMutex
	apiAddr string
	secret  string
}

func NewAPIClient(cfg *config.Manager, st *state.RuntimeState) *APIClient {
	return &APIClient{
		cfg: cfg,
		st:  st,
		httpClient: &http.Client{
			Transport: &http.Transport{
				Proxy: nil,
				DialContext: (&net.Dialer{
					Timeout:       2 * time.Second,
					KeepAlive:     30 * time.Second,
					FallbackDelay: 10 * time.Millisecond,
				}).DialContext,
				MaxIdleConns:          100,
				MaxIdleConnsPerHost:   100,
				IdleConnTimeout:       90 * time.Second,
				ResponseHeaderTimeout: 12 * time.Second,
			},
		},
	}
}

func (c *APIClient) SetEndpoint(addr, secret string) {
	c.connMu.Lock()
	defer c.connMu.Unlock()
	
	addr = strings.TrimSuffix(addr, "/")
	if strings.HasPrefix(addr, "0.0.0.0:") {
		addr = strings.Replace(addr, "0.0.0.0:", "127.0.0.1:", 1)
	} else if strings.HasPrefix(addr, "[::]:") {
		addr = strings.Replace(addr, "[::]:", "127.0.0.1:", 1)
	}
	if !strings.HasPrefix(addr, "http") && addr != "" {
		addr = "http://" + addr
	}
	
	c.apiAddr = addr
	c.secret = secret
}

func (c *APIClient) GetEndpoint() (string, string) {
	c.connMu.RLock()
	defer c.connMu.RUnlock()
	rawAddr := strings.TrimPrefix(c.apiAddr, "http://")
	return rawAddr, c.secret
}

func (c *APIClient) DoRequest(ctx context.Context, method, path string, payload interface{}) ([]byte, error) {
	if c.st.IsExiting() {
		return nil, context.Canceled
	}

	c.connMu.RLock()
	targetAddr := c.apiAddr
	targetSecret := c.secret
	c.connMu.RUnlock()

	if targetAddr == "" {
		return nil, fmt.Errorf("api endpoint is not initialized")
	}

	url := targetAddr + "/" + strings.TrimPrefix(path, "/")

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
	if targetSecret != "" {
		req.Header.Set("Authorization", "Bearer "+targetSecret)
	}

	if !(method == http.MethodGet && path == "/configs") {
		slog.Debug("发送内核 API 请求", "method", method, "path", path)
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
		slog.Error("内核 API 响应为空且状态异常", "code", resp.StatusCode)
		return nil, fmt.Errorf("API Status Error: %d", resp.StatusCode)
	}

	limitReader := io.LimitReader(resp.Body, 5*1024*1024)
	body, err := io.ReadAll(limitReader)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errMsg := strings.TrimSpace(string(body))
		if strings.HasPrefix(errMsg, "{") {
			var errObj map[string]interface{}
			if json.Unmarshal([]byte(errMsg), &errObj) == nil {
				if msg, ok := errObj["message"].(string); ok {
					errMsg = msg
				}
			}
		}
		
		slog.Debug("内核 API 拒绝请求", "code", resp.StatusCode, "detail", errMsg)
		return nil, fmt.Errorf("API Error %d: %s", resp.StatusCode, errMsg)
	}

	return body, nil
}

func (c *APIClient) SyncConfigToKernel(ctx context.Context, payload map[string]interface{}) error {
	if c.st.IsExiting() {
		return context.Canceled
	}
	_, err := c.DoRequest(ctx, http.MethodPatch, "/configs", payload)
	return err
}
