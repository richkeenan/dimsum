//go:build linux

package dhcp

import (
	"fmt"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

func openDiagnosticSocket(name string) (*net.UDPConn, error) {
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return nil, err
	}
	if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 || len(iface.HardwareAddr) != 6 {
		return nil, fmt.Errorf("probe requires an up Ethernet interface")
	}
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, unix.IPPROTO_UDP)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), "dhcp-diagnostic")
	defer file.Close()
	if err = unix.SetsockoptString(fd, unix.SOL_SOCKET, unix.SO_BINDTODEVICE, name); err != nil {
		return nil, err
	}
	if err = unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_BROADCAST, 1); err != nil {
		return nil, err
	}
	if err = unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_RCVBUF, 16<<10); err != nil {
		return nil, err
	}
	if err = unix.Bind(fd, &unix.SockaddrInet4{Port: 68}); err != nil {
		return nil, err
	}
	conn, err := net.FilePacketConn(file)
	if err != nil {
		return nil, err
	}
	return conn.(*net.UDPConn), nil
}
