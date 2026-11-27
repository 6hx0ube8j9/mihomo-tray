package webui

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/netutil"
)

const (
	fallbackDebugPort = "53819"
	cdpHost           = domain.LocalhostIP
	cdpWSDialTimeout  = 500 * time.Millisecond
	cdpWSOpTimeout    = 1500 * time.Millisecond
)

var cdpClient = &http.Client{
	Timeout: 300 * time.Millisecond,
	Transport: &http.Transport{
		Proxy:             nil,
		DisableKeepAlives: true,
	},
}

type cdpTarget struct {
	ID                   string `json:"id"`
	Type                 string `json:"type"`
	Title                string `json:"title"`
	URL                  string `json:"url"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
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

func reloadCDPPageTargets(debugPort string) error {
	targets, err := getCDPTargets(debugPort)
	if err != nil {
		return err
	}

	var lastErr error
	reloaded := 0
	for _, t := range targets {
		if t.Type != "" && t.Type != "page" {
			continue
		}
		if t.URL == "" || t.URL == "about:blank" {
			continue
		}

		wsPath := resolveCDPTargetWSPath(t)
		if wsPath == "" {
			continue
		}
		if err := sendCDPCommand(debugPort, wsPath, `{"id":1,"method":"Page.reload"}`); err != nil {
			lastErr = err
			continue
		}
		reloaded++
	}

	if reloaded == 0 && lastErr != nil {
		return lastErr
	}
	return nil
}

func resolveCDPTargetWSPath(t cdpTarget) string {
	if t.WebSocketDebuggerURL != "" {
		if u, err := url.Parse(t.WebSocketDebuggerURL); err == nil && u.RequestURI() != "" {
			return u.RequestURI()
		}
	}
	if t.ID != "" {
		return "/devtools/page/" + t.ID
	}
	return ""
}

func sendCDPCommand(debugPort, wsPath, jsonCmd string) error {
	addr := net.JoinHostPort(cdpHost, debugPort)
	conn, err := net.DialTimeout("tcp", addr, cdpWSDialTimeout)
	if err != nil {
		return err
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(cdpWSOpTimeout))

	var keyBytes [16]byte
	if _, err := rand.Read(keyBytes[:]); err != nil {
		return err
	}
	secKey := base64.StdEncoding.EncodeToString(keyBytes[:])

	handshake := fmt.Sprintf(
		"GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n",
		wsPath, addr, secKey,
	)
	if _, err := io.WriteString(conn, handshake); err != nil {
		return err
	}

	br := bufio.NewReader(conn)
	statusLine, err := br.ReadString('\n')
	if err != nil {
		return err
	}
	if !strings.Contains(statusLine, " 101 ") {
		return fmt.Errorf("CDP WebSocket 握手失败: %s", strings.TrimSpace(statusLine))
	}
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return err
		}
		if line == "\r\n" || line == "\n" {
			break
		}
	}

	frame, err := encodeWSClientTextFrame([]byte(jsonCmd))
	if err != nil {
		return err
	}
	if _, err := conn.Write(frame); err != nil {
		return err
	}

	// Read the 2-byte server frame header to confirm Chromium handled the command before closing.
	var ackHdr [2]byte
	if _, err := io.ReadFull(br, ackHdr[:]); err != nil {
		return err
	}
	return nil
}

func encodeWSClientTextFrame(payload []byte) ([]byte, error) {
	n := len(payload)
	if n > 65535 {
		return nil, errors.New("CDP payload too large")
	}

	var mask [4]byte
	if _, err := rand.Read(mask[:]); err != nil {
		return nil, err
	}

	var frame []byte
	var offset int
	if n < 126 {
		frame = make([]byte, 2+4+n)
		frame[0] = 0x81
		frame[1] = 0x80 | byte(n)
		offset = 2
	} else {
		frame = make([]byte, 4+4+n)
		frame[0] = 0x81
		frame[1] = 0x80 | 126
		binary.BigEndian.PutUint16(frame[2:4], uint16(n))
		offset = 4
	}

	copy(frame[offset:offset+4], mask[:])
	dataStart := offset + 4
	for i, b := range payload {
		frame[dataStart+i] = b ^ mask[i%4]
	}
	return frame, nil
}
