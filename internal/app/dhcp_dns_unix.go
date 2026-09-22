//go:build unix

package app

import (
	"net/netip"
	"syscall"

	"golang.org/x/sys/unix"
)

func dnsIPv4Address(socket any) (netip.AddrPort, bool) {
	c, ok := socket.(syscall.Conn)
	if !ok {
		return netip.AddrPort{}, false
	}
	raw, err := c.SyscallConn()
	if err != nil {
		return netip.AddrPort{}, false
	}
	var address netip.AddrPort
	err = raw.Control(func(fd uintptr) {
		bound, e := unix.Getsockname(int(fd))
		if e != nil {
			return
		}
		switch a := bound.(type) {
		case *unix.SockaddrInet4:
			address = netip.AddrPortFrom(netip.AddrFrom4(a.Addr), uint16(a.Port))
		case *unix.SockaddrInet6:
			if a.Addr != [16]byte{} {
				return
			}
			only, e := unix.GetsockoptInt(int(fd), unix.IPPROTO_IPV6, unix.IPV6_V6ONLY)
			if e == nil && only == 0 {
				address = netip.AddrPortFrom(netip.IPv4Unspecified(), uint16(a.Port))
			}
		}
	})
	return address, err == nil && address.IsValid()
}
