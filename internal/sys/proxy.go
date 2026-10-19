package sys

import (
	"strings"
)

const DefaultProxyOverride = "<local>;localhost;127.*;10.*;172.16.*;172.17.*;172.18.*;172.19.*;172.20.*;172.21.*;172.22.*;172.23.*;172.24.*;172.25.*;172.26.*;172.27.*;172.28.*;172.29.*;172.30.*;172.31.*;192.168.*"

type ProxyStatus struct {
	Enabled bool
	Server  string
}

func (p ProxyStatus) Matches(expected string) bool {
	return IsServerMatch(p.Server, expected)
}

func IsServerMatch(currServer, expected string) bool {
	curr := NormalizeProxyServer(currServer)
	exp := NormalizeProxyServer(expected)

	if curr == "" || exp == "" {
		return false
	}

	if strings.EqualFold(curr, exp) {
		return true
	}

	for _, part := range strings.Split(currServer, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, after, ok := strings.Cut(part, "="); ok {
			part = after
		}
		if strings.EqualFold(NormalizeProxyServer(part), exp) {
			return true
		}
	}

	return false
}

func NormalizeProxyServer(server string) string {
	s := strings.TrimSpace(server)
	if s == "" {
		return ""
	}

	lower := strings.ToLower(s)
	for _, scheme := range []string{"http://", "https://", "socks://", "socks5://"} {
		if strings.HasPrefix(lower, scheme) {
			s = s[len(scheme):]
			break
		}
	}
	s = strings.TrimSpace(s)

	if strings.HasPrefix(s, ":") {
		return "127.0.0.1" + s
	}
	if !strings.Contains(s, ":") {
		return "127.0.0.1:" + s
	}
	return s
}
