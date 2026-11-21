package webui

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"

	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/netutil"
)

const (
	fallbackDebugPort = "53819"
	cdpHost           = domain.LocalhostIP
)

var cdpClient = &http.Client{
	Timeout: 300 * time.Millisecond,
	Transport: &http.Transport{
		Proxy:             nil,
		DisableKeepAlives: true,
	},
}

type cdpTarget struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

func buildCDPBaseURL(port string) string {
	return fmt.Sprintf("http://%s/json", net.JoinHostPort(cdpHost, port))
}

func buildCDPActionURL(port, action, targetID string) string {
	return fmt.Sprintf("http://%s/json/%s/%s", net.JoinHostPort(cdpHost, port), action, targetID)
}

func allocateCDPPort() string {
	port, err := netutil.GetFreePort(cdpHost)
	if err != nil {
		return fallbackDebugPort
	}
	return port
}

func getCDPTargets(debugPort string) ([]cdpTarget, error) {
	resp, err := cdpClient.Get(buildCDPBaseURL(debugPort))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("CDP 返回异常状态码: %d", resp.StatusCode)
	}

	var targets []cdpTarget
	if err := json.NewDecoder(resp.Body).Decode(&targets); err != nil {
		return nil, err
	}
	return targets, nil
}

func isCDPAlive(port string) bool {
	targets, err := getCDPTargets(port)
	return err == nil && targets != nil
}

func findCDPPageTarget(debugPort string) (id string, title string, found bool) {
	targets, err := getCDPTargets(debugPort)
	if err != nil {
		return "", "", false
	}

	for _, t := range targets {
		if t.Type != "" && t.Type != "page" {
			continue
		}
		if t.URL != "" && t.URL != "about:blank" {
			return t.ID, t.Title, true
		}
	}
	return "", "", false
}

func activateCDPTarget(debugPort, targetID string) error {
	resp, err := cdpClient.Get(buildCDPActionURL(debugPort, "activate", targetID))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

func closeAllCDPTargets(debugPort string) {
	targets, err := getCDPTargets(debugPort)
	if err != nil {
		return
	}
	for _, t := range targets {
		if t.ID != "" {
			if closeResp, closeErr := cdpClient.Get(buildCDPActionURL(debugPort, "close", t.ID)); closeErr == nil {
				_ = closeResp.Body.Close()
			}
		}
	}
}
