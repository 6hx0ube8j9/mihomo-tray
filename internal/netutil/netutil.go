package netutil

import (
	"net"
)

func IsPublicAddress(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}

	return host == "0.0.0.0" || host == "::" || host == "[::]" || host == ""
}
