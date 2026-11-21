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
type mibTCPRowOwnerPID struct {
	dwState      uint32
	dwLocalAddr  uint32
	dwLocalPort  uint32
	dwRemoteAddr uint32
	dwRemotePort uint32
	dwOwningPid  uint32
}

// MIB_TCP6ROW_OWNER_PID (IPv6)
type mibTCP6RowOwnerPID struct {
	ucLocalAddr     [16]byte
	dwLocalScopeId  uint32
	dwLocalPort     uint32
	ucRemoteAddr    [16]byte
	dwRemoteScopeId uint32
	dwRemotePort    uint32
	dwState         uint32
	dwOwningPid     uint32
}

func FindTCPListenerPID(port string) uint32 {
	portNum, err := strconv.ParseUint(strings.TrimSpace(port), 10, 16)
	if err != nil || portNum == 0 {
		return 0
	}
	targetPort := uint16(portNum)

	if pid := findTCP4ListenerPID(targetPort); pid != 0 {
		return pid
	}
	return findTCP6ListenerPID(targetPort)
}

func findTCP4ListenerPID(targetPort uint16) uint32 {
	buf, ok := queryExtendedTCPTable(afInet)
	if !ok || len(buf) < 1 {
		return 0
	}

	numEntries := buf[0]
	if numEntries == 0 {
		return 0
	}

	rowSize := unsafe.Sizeof(mibTCPRowOwnerPID{})
	if uintptr(numEntries) > uintptr(len(buf)-1)*4/rowSize {
		return 0
	}

	rows := unsafe.Slice((*mibTCPRowOwnerPID)(unsafe.Pointer(&buf[1])), numEntries)
	for i := range rows {
		port := uint16(((rows[i].dwLocalPort & 0xFF) << 8) | ((rows[i].dwLocalPort >> 8) & 0xFF))
		if port == targetPort && rows[i].dwOwningPid != 0 {
			return rows[i].dwOwningPid
		}
	}
	return 0
}

func findTCP6ListenerPID(targetPort uint16) uint32 {
	buf, ok := queryExtendedTCPTable(afInet6)
	if !ok || len(buf) < 1 {
		return 0
	}

	numEntries := buf[0]
	if numEntries == 0 {
		return 0
	}

	rowSize := unsafe.Sizeof(mibTCP6RowOwnerPID{})
	if uintptr(numEntries) > uintptr(len(buf)-1)*4/rowSize {
		return 0
	}

	rows := unsafe.Slice((*mibTCP6RowOwnerPID)(unsafe.Pointer(&buf[1])), numEntries)
	for i := range rows {
		port := uint16(((rows[i].dwLocalPort & 0xFF) << 8) | ((rows[i].dwLocalPort >> 8) & 0xFF))
		if port == targetPort && rows[i].dwOwningPid != 0 {
			return rows[i].dwOwningPid
		}
	}
	return 0
}

func queryExtendedTCPTable(af uint32) ([]uint32, bool) {
	var size uint32
	ret, _, _ := procGetExtendedTcpTable.Call(
		0,
		uintptr(unsafe.Pointer(&size)),
		0,
		uintptr(af),
		uintptr(tcpTableOwnerPidListener),
		0,
	)
	if ret != errorInsufficientBuffer || size < 4 {
		return nil, false
	}

	for i := 0; i < 3; i++ {
		if size < 4 {
			return nil, false
		}
		buf := make([]uint32, (size+3)/4)
		bufSize := uint32(len(buf) * 4)
		ret, _, _ := procGetExtendedTcpTable.Call(
			uintptr(unsafe.Pointer(&buf[0])),
			uintptr(unsafe.Pointer(&bufSize)),
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
		size = bufSize
	}
	return nil, false
}
