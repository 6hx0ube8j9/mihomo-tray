package webui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"mihomo-tray/internal/netutil"
)

const (
	fallbackDebugPort1 = "52819"
	fallbackDebugPort2 = "52820"
	cdpHost            = domain.LocalhostIP
)

var cdpClient = &http.Client{
	Transport: &http.Transport{
		DisableKeepAlives: true,
	},
}

func buildCDPBaseURL(port string) string {
	return fmt.Sprintf("http://%s:%s/json", cdpHost, port)
}

func buildCDPActionURL(port, action, targetID string) string {
	return fmt.Sprintf("http://%s:%s/json/%s/%s", cdpHost, port, action, targetID)
}

func getCDPDebugPort() string {
	port, err := netutil.GetFreePort(cdpHost)
	if err != nil {
		return fallbackDebugPort1
	}
	return port
}

func safeGet(url string) (*http.Response, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	return cdpClient.Do(req)
}

func getCDPTargets(debugPort string) ([]map[string]interface{}, error) {
	resp, err := safeGet(buildCDPBaseURL(debugPort))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var targets []map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&targets); err != nil {
		return nil, err
	}
	return targets, nil
}

func IsDebugPortAlive(port string) bool {
	targets, err := getCDPTargets(port)
	return err == nil && targets != nil
}

func GetWebUITarget(debugPort string) (id string, title string, found bool) {
	targets, err := getCDPTargets(debugPort)
	if err != nil {
		return "", "", false
	}

	for _, t := range targets {
		pURL, _ := t["url"].(string)
		if strings.Contains(pURL, "/ui/") || strings.Contains(pURL, "setup") || strings.Contains(pURL, "#/proxies") || strings.Contains(pURL, "board.zash") {
			id, _ = t["id"].(string)
			title, _ = t["title"].(string)
			return id, title, true
		}
	}
	return "", "", false
}

func ActivateTarget(debugPort, targetID string) error {
	resp, err := safeGet(buildCDPActionURL(debugPort, "activate", targetID))
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	return nil
}

func CloseAllWebUITargets(debugPort string) {
	targets, err := getCDPTargets(debugPort)
	if err != nil {
		return
	}
	for _, t := range targets {
		if id, ok := t["id"].(string); ok {
			if closeResp, closeErr := safeGet(buildCDPActionURL(debugPort, "close", id)); closeErr == nil {
				_ = closeResp.Body.Close()
			}
		}
	}
}
