//go:build windows

package sys

import (
	"strconv"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modIphlpapi             = windows.NewLazySystemDLL("iphlpapi.dll")
	procGetExtendedTcpTable = modIphlpapi.NewProc("GetExtendedTcpTable")
)

const (
	afInet                   = 2  // AF_INET (IPv4)
	afInet6                  = 23 // AF_INET6 (IPv6)
	tcpTableOwnerPidListener = 3  // TCP_TABLE_OWNER_PID_LISTENER
	errorInsufficientBuffer  = 122
)

// MIB_TCPROW_OWNER_PID (IPv4)
type mibTcpRowOwnerPid struct {
	dwState      uint32
	dwLocalAddr  uint32
	dwLocalPort  uint32
	dwRemoteAddr uint32
	dwRemotePort uint32
	dwOwningPid  uint32
}

// MIB_TCP6ROW_OWNER_PID (IPv6)
type mibTcp6RowOwnerPid struct {
	ucLocalAddr     [16]byte
	dwLocalScopeId  uint32
	dwLocalPort     uint32
	ucRemoteAddr    [16]byte
	dwRemoteScopeId uint32
	dwRemotePort    uint32
	dwState         uint32
	dwOwningPid     uint32
}

func GetProcessIdByPort(port string) uint32 {
	portNum, err := strconv.ParseUint(strings.TrimSpace(port), 10, 16)
	if err != nil || portNum == 0 {
		return 0
	}
	targetPort := uint16(portNum)

	if pid := findTcp4ListenerPid(targetPort); pid != 0 {
		return pid
	}

	return findTcp6ListenerPid(targetPort)
}

func findTcp4ListenerPid(targetPort uint16) uint32 {
	buf, ok := queryExtendedTcpTable(afInet)
	if !ok || len(buf) < 4 {
		return 0
	}

	numEntries := *(*uint32)(unsafe.Pointer(&buf[0]))
	if numEntries == 0 {
		return 0
	}

	rowSize := unsafe.Sizeof(mibTcpRowOwnerPid{})
	if uintptr(numEntries) > (uintptr(len(buf))-4)/rowSize {
		return 0
	}

	rows := unsafe.Slice((*mibTcpRowOwnerPid)(unsafe.Pointer(&buf[4])), numEntries)
	for i := range rows {
		port := ((rows[i].dwLocalPort & 0xFF) << 8) | ((rows[i].dwLocalPort >> 8) & 0xFF)
		if uint16(port) == targetPort {
			return rows[i].dwOwningPid
		}
	}
	return 0
}

func findTcp6ListenerPid(targetPort uint16) uint32 {
	buf, ok := queryExtendedTcpTable(afInet6)
	if !ok || len(buf) < 4 {
		return 0
	}

	numEntries := *(*uint32)(unsafe.Pointer(&buf[0]))
	if numEntries == 0 {
		return 0
	}

	rowSize := unsafe.Sizeof(mibTcp6RowOwnerPid{})
	if uintptr(numEntries) > (uintptr(len(buf))-4)/rowSize {
		return 0
	}

	rows := unsafe.Slice((*mibTcp6RowOwnerPid)(unsafe.Pointer(&buf[4])), numEntries)
	for i := range rows {
		port := ((rows[i].dwLocalPort & 0xFF) << 8) | ((rows[i].dwLocalPort >> 8) & 0xFF)
		if uint16(port) == targetPort {
			return rows[i].dwOwningPid
		}
	}
	return 0
}

func queryExtendedTcpTable(af uint32) ([]byte, bool) {
	var size uint32
	procGetExtendedTcpTable.Call(
		0,
		uintptr(unsafe.Pointer(&size)),
		0,
		uintptr(af),
		uintptr(tcpTableOwnerPidListener),
		0,
	)
	if size == 0 {
		return nil, false
	}

	for i := 0; i < 3; i++ {
		if size == 0 {
			return nil, false
		}
		buf := make([]byte, size)
		ret, _, _ := procGetExtendedTcpTable.Call(
			uintptr(unsafe.Pointer(&buf[0])),
			uintptr(unsafe.Pointer(&size)),
			0,
			uintptr(af),
			uintptr(tcpTableOwnerPidListener),
			0,
		)
		if ret == 0 {
			return buf, true
		}
		if ret != errorInsufficientBuffer {
			return nil, false
		}
	}
	return nil, false
}
