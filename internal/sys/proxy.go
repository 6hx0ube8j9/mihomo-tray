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
	if strings.EqualFold(currServer, expected) {
		return true
	}
	for _, part := range strings.Split(currServer, ";") {
		part = strings.TrimSpace(part)
		if _, after, ok := strings.Cut(part, "="); ok {
			if strings.EqualFold(strings.TrimSpace(after), expected) {
				return true
			}
		}
	}
	return false
}

func NormalizeProxyServer(server string) string {
	server = strings.TrimSpace(server)
	if server != "" && !strings.Contains(server, ":") {
		return "127.0.0.1:" + server
	}
	return server
}
