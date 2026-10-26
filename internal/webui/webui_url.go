package webui

import (
	"fmt"
	"net"
	"net/url"
	"strings"

	"mihomo-tray/internal/domain"
)

const defaultWebUIHost = domain.LocalhostIP

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
			port = domain.DefaultExternalControllerPort
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

func buildRemoteWebUIURL(customURL, host, port, secret string) string {
	rawURL := strings.TrimSpace(customURL)
	if rawURL == "" {
		rawURL = strings.TrimSpace(domain.DefaultRemoteWebUIURL)
	}

	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		rawURL = "https://" + rawURL
	}
	rawURL = strings.TrimRight(rawURL, "/?")

	query := buildQueryArgs(host, port, secret)
	
	cleanBase := rawURL
	if idx := strings.IndexAny(cleanBase, "?#"); idx != -1 {
		cleanBase = cleanBase[:idx]
	}
	baseLower := strings.ToLower(cleanBase)

	if strings.Contains(baseLower, "board.zash.run.place") {
		return cleanBase + "/#/setup?http=true&" + query
	}

	if strings.Contains(baseLower, "metacubexd") || strings.Contains(baseLower, "metacubex.github.io") {
		if strings.HasSuffix(baseLower, "metacubex.github.io") {
			return cleanBase + "/metacubexd/#/setup?http=true&" + query
		}
		return cleanBase + "/#/setup?http=true&" + query
	}

	if strings.Contains(baseLower, "yacd") {
		return cleanBase + "/?" + query
	}

	hasHash := strings.Contains(rawURL, "#")
	hasQuery := strings.Contains(rawURL, "?")

	switch {
	case hasHash && hasQuery:
		return rawURL + "&" + query
	case hasHash || hasQuery:
		return rawURL + "?" + query
	default:
		return rawURL + "/#/setup?" + query
	}
}

func buildFinalURL(cfg Config) (string, string) {
	host, port, appHostPort := parseAPIAddress(cfg.APIAddr)

	var finalURL string
	if cfg.RemoteWebUI {
		finalURL = buildRemoteWebUIURL(cfg.RemoteWebUIURL, host, port, cfg.Secret)
	} else {
		finalURL = buildLocalWebUIURL(host, port, cfg.Secret, cfg.UIName)
	}

	return finalURL, appHostPort
}
