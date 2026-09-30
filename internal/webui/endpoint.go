package webui

import (
	"fmt"
	"net"
	"net/url"
	"strings"

	"mihomo-tray/internal/domain"
)

const defaultWebUIHost = "127.0.0.1"

func parseAPIAddress(apiAddr string) (host string, port string, appHostPort string) {
	cleanAddr := strings.TrimRight(apiAddr, "/")
	cleanAddr = strings.TrimPrefix(strings.TrimPrefix(cleanAddr, "http://"), "https://")

	var err error
	host, port, err = net.SplitHostPort(cleanAddr)
	if err != nil {
		host = cleanAddr
	}

	if port == "" {
		_, defaultPort, _ := net.SplitHostPort(domain.DefaultExternalController)
		port = defaultPort
		if port == "" {
			port = "9090"
		}
	}

	if host == "" {
		host = defaultWebUIHost
	} else if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
		host = defaultWebUIHost
	}

	return host, port, net.JoinHostPort(host, port)
}

func buildQueryArgs(host, port, secret string) string {
	q := url.Values{}
	q.Set("hostname", host)
	q.Set("port", port)
	if secret != "" {
		q.Set("secret", secret)
	}
	return q.Encode()
}

func buildLocalWebUIURL(host, port, secret, uiName string) string {
	uiPath := "/ui/"
	if uiName != "" {
		uiPath = fmt.Sprintf("/ui/%s/", strings.Trim(uiName, "/"))
	}
	query := buildQueryArgs(host, port, secret)
	return fmt.Sprintf("http://%s:%s%s?%s#/setup?%s", host, port, uiPath, query, query)
}

func buildRemoteWebUIURL(host, port, secret string) string {
	query := buildQueryArgs(host, port, secret)

	baseURL := strings.TrimSpace(domain.DefaultRemoteWebUIURL)

	if strings.Contains(baseURL, "?") {
		return baseURL + "&" + query
	}
	return baseURL + "?" + query
}

func buildFinalURL(cfg Config) (string, string) {
	host, port, appHostPort := parseAPIAddress(cfg.APIAddr)

	var finalURL string
	if cfg.RemoteWebUI {
		finalURL = buildRemoteWebUIURL(host, port, cfg.Secret)
	} else {
		finalURL = buildLocalWebUIURL(host, port, cfg.Secret, cfg.UIName)
	}

	return finalURL, appHostPort
}
