package netutil

import (
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
)

var (
	ErrEmptyPort   = errors.New("empty port")
	ErrInvalidType = errors.New("invalid port format")
	ErrOutOfRange  = errors.New("port out of range")
)

func ParseAndValidatePort(portStr string) (int, error) {
	portStr = strings.TrimSpace(portStr)
	if portStr == "" {
		return 0, ErrEmptyPort
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return 0, ErrInvalidType
	}
	if port < 0 || port > 65535 {
		return 0, ErrOutOfRange
	}
	return port, nil
}

func IsValidHostPort(addr string) bool {
	addr = strings.TrimSpace(addr)
	if addr == "" || strings.ContainsAny(addr, " \t\r\n") {
		return false
	}
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	_, err = ParseAndValidatePort(portStr)
	return err == nil
}

func IsPublicAddress(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	return host == "0.0.0.0" || host == "::" || host == "[::]" || host == ""
}

func GetFreePort(host string) (string, error) {
	addr, err := net.ResolveTCPAddr("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		return "", err
	}

	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return "", err
	}

	port := strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
	_ = l.Close()
	return port, nil
}

func IsValidHTTPURL(rawURL string) bool {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return false
	}
	u, err := url.ParseRequestURI(rawURL)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	if u.Host == "" {
		return false
	}
	return true
}
