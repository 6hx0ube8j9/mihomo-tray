package netutil

import (
	"net"
	"strconv"
)

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
