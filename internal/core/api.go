package core

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "io"
    "net"
    "net/http"
    "os"
    "path/filepath"
    "strings"
    "time"

    "mihomo-tray/internal/fsm"
)

type APIClient struct {
    cm         *fsm.Manager
    httpClient *http.Client
}

func NewAPIClient(cm *fsm.Manager) *APIClient {
    return &APIClient{
        cm: cm,
        httpClient: &http.Client{
            Transport: &http.Transport{
                Proxy: nil,
                DialContext: (&net.Dialer{
                    Timeout:   200 * time.Millisecond,
                    KeepAlive: 30 * time.Second,
                    FallbackDelay: 10 * time.Millisecond,
                }).DialContext,
                MaxIdleConns:          100,
                MaxIdleConnsPerHost:   100,
                IdleConnTimeout:       90 * time.Second,
                ResponseHeaderTimeout: 2 * time.Second,
            },
        },
    }
}

func (c *APIClient) DoRequest(ctx context.Context, method, path string, payload interface{}) ([]byte, error) {
    if c.cm.State.IsExiting() {
        return nil, context.Canceled
    }

    apiAddr := strings.TrimSuffix(c.cm.Get("external-controller"), "/")
    if apiAddr == "" {
        return nil, fmt.Errorf("api address is empty")
    }
    if !strings.HasPrefix(apiAddr, "http") {
        apiAddr = "http://" + apiAddr
    }
    url := apiAddr + "/" + strings.TrimPrefix(path, "/")

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
    if secret := c.cm.Get("secret"); secret != "" {
        req.Header.Set("Authorization", "Bearer "+secret)
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

    limitReader := io.LimitReader(resp.Body, 5*1024*1024)
    body, err := io.ReadAll(limitReader)
    if err != nil {
        return nil, err
    }

    if resp.StatusCode < 200 || resp.StatusCode >= 300 {
        if method == http.MethodPut && strings.Contains(path, "/configs") {
            logPath := filepath.Join(c.cm.BaseDir(), "error.log")
            _ = os.WriteFile(logPath, body, 0644)
        }
        return body, fmt.Errorf("API Error: %d, Response: %s", resp.StatusCode, string(body))
    }

    return body, nil
}

func (c *APIClient) SyncConfigToKernel(ctx context.Context, payload map[string]interface{}) error {
    if c.cm.State.IsExiting() {
        return context.Canceled
    }
    _, err := c.DoRequest(ctx, http.MethodPatch, "/configs", payload)
    return err
}
