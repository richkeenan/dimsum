//go:build linux

package dhcp

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"net/netip"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/mdlayher/packet"
	"golang.org/x/net/bpf"
	"golang.org/x/sys/unix"
)

type systemLink struct {
	udp    *net.UDPConn
	sender *packet.Conn
	iface  *net.Interface
	source netip.Addr
	once   sync.Once
}

// OpenSystemLink validates the configured address and opens exclusive UDP/67
// ingress plus direct L2 egress. It never sends traffic during preparation.
func OpenSystemLink(s Settings) (Link, ProbeFunc, error) {
	if !s.Enabled {
		return nil, nil, fmt.Errorf("dhcp: cannot open disabled packet service")
	}
	if err := ValidateSettings(s); err != nil {
		return nil, nil, err
	}
	iface, err := net.InterfaceByName(s.Interface)
	if err != nil {
		return nil, nil, err
	}
	if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 || len(iface.HardwareAddr) != 6 || iface.MTU < 576 {
		return nil, nil, fmt.Errorf("dhcp: interface must be up Ethernet with MTU >=576")
	}
	source := netip.MustParseAddr(s.ServerIP)
	prefix := netip.MustParsePrefix(s.Subnet)
	addrs, err := iface.Addrs()
	if err != nil {
		return nil, nil, err
	}
	found := false
	for _, a := range addrs {
		p, e := netip.ParsePrefix(a.String())
		if e == nil && p.Addr() == source && p.Bits() == prefix.Bits() {
			found = true
		}
	}
	if !found {
		return nil, nil, fmt.Errorf("dhcp: configured server address/subnet is not installed on interface")
	}
	permanent, err := permanentAddress(iface.Index, source)
	if err != nil {
		return nil, nil, err
	}
	if !permanent {
		return nil, nil, fmt.Errorf("dhcp: server address must be permanent/static")
	}
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, unix.IPPROTO_UDP)
	if err != nil {
		return nil, nil, err
	}
	file := os.NewFile(uintptr(fd), "dhcp-udp")
	defer file.Close()
	if err = unix.SetsockoptString(fd, unix.SOL_SOCKET, unix.SO_BINDTODEVICE, s.Interface); err != nil {
		return nil, nil, err
	}
	if err = unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_RCVBUF, 64<<10); err != nil {
		return nil, nil, err
	}
	if err = unix.Bind(fd, &unix.SockaddrInet4{Port: 67}); err != nil {
		return nil, nil, err
	}
	conn, err := net.FilePacketConn(file)
	if err != nil {
		return nil, nil, err
	}
	link := &systemLink{udp: conn.(*net.UDPConn), iface: iface, source: source}
	ok := false
	defer func() {
		if !ok {
			link.Close()
		}
	}()
	link.sender, err = packet.Listen(iface, packet.Datagram, unix.ETH_P_IP, nil)
	if err != nil {
		return nil, nil, err
	}
	// Egress-only socket: do not accumulate a raw receive backlog.
	if err = link.sender.SetBPF([]bpf.RawInstruction{{Op: 0x06, K: 0}}); err != nil {
		return nil, nil, err
	}
	arp, err := openARP(iface)
	if err != nil {
		return nil, nil, err
	}
	_ = arp.Close()
	ok = true
	return link, func(ctx context.Context, a netip.Addr) (bool, error) { return probeARP(ctx, iface, a) }, nil
}

func permanentAddress(index int, address netip.Addr) (bool, error) {
	rib, err := syscall.NetlinkRIB(unix.RTM_GETADDR, unix.AF_INET)
	if err != nil {
		return false, err
	}
	messages, err := syscall.ParseNetlinkMessage(rib)
	if err != nil {
		return false, err
	}
	for _, message := range messages {
		if message.Header.Type != unix.RTM_NEWADDR || len(message.Data) < 8 || int(binary.NativeEndian.Uint32(message.Data[4:8])) != index {
			continue
		}
		attrs, err := syscall.ParseNetlinkRouteAttr(&message)
		if err != nil {
			return false, err
		}
		flags := uint32(message.Data[2])
		matches := false
		forever := true
		for _, attr := range attrs {
			switch attr.Attr.Type {
			case unix.IFA_LOCAL:
				matches = bytes.Equal(attr.Value, address.AsSlice())
			case unix.IFA_FLAGS:
				if len(attr.Value) >= 4 {
					flags = binary.NativeEndian.Uint32(attr.Value[:4])
				}
			case unix.IFA_CACHEINFO:
				if len(attr.Value) >= 8 {
					forever = binary.NativeEndian.Uint32(attr.Value[:4]) == ^uint32(0) && binary.NativeEndian.Uint32(attr.Value[4:8]) == ^uint32(0)
				}
			}
		}
		if matches {
			return flags&unix.IFA_F_PERMANENT != 0 && forever, nil
		}
	}
	return false, nil
}
func (l *systemLink) MTU() int { return l.iface.MTU }
func (l *systemLink) Close() error {
	l.once.Do(func() {
		if l.udp != nil {
			_ = l.udp.Close()
		}
		if l.sender != nil {
			_ = l.sender.Close()
		}
	})
	return nil
}
func (l *systemLink) Receive(b []byte) (int, netip.AddrPort, bool, error) {
	n, _, flags, peer, err := l.udp.ReadMsgUDPAddrPort(b, nil)
	return n, peer, flags&unix.MSG_TRUNC != 0, err
}
func (l *systemLink) Send(ctx context.Context, w WireReply) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(w.Payload)+28 > l.iface.MTU || len(w.Payload) > MaxDatagram || !w.Destination.Is4() {
		return fmt.Errorf("dhcp: invalid/oversized egress")
	}
	b := make([]byte, 28+len(w.Payload))
	b[0] = 0x45
	b[8] = 64
	b[9] = 17
	binary.BigEndian.PutUint16(b[2:4], uint16(len(b)))
	copy(b[12:16], l.source.AsSlice())
	copy(b[16:20], w.Destination.AsSlice())
	var sum uint32
	for i := 0; i < 20; i += 2 {
		sum += uint32(binary.BigEndian.Uint16(b[i : i+2]))
	}
	for sum > 65535 {
		sum = (sum & 65535) + (sum >> 16)
	}
	binary.BigEndian.PutUint16(b[10:12], ^uint16(sum))
	binary.BigEndian.PutUint16(b[20:22], 67)
	binary.BigEndian.PutUint16(b[22:24], 68)
	binary.BigEndian.PutUint16(b[24:26], uint16(8+len(w.Payload)))
	// Zero UDP checksum is permitted for IPv4. No fragmentation is emitted.
	copy(b[28:], w.Payload)
	deadline := time.Now().Add(50 * time.Millisecond)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := l.sender.SetWriteDeadline(deadline); err != nil {
		return err
	}
	_, err := l.sender.WriteTo(b, &packet.Addr{HardwareAddr: net.HardwareAddr(w.MAC[:])})
	return err
}
func openARP(iface *net.Interface) (*packet.Conn, error) {
	c, err := packet.Listen(iface, packet.Datagram, unix.ETH_P_ARP, nil)
	if err != nil {
		return nil, err
	}
	raw, err := c.SyscallConn()
	if err == nil {
		var socketErr error
		err = raw.Control(func(fd uintptr) { socketErr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_RCVBUF, 16<<10) })
		if err == nil {
			err = socketErr
		}
	}
	if err == nil {
		err = c.SetBPF([]bpf.RawInstruction{{Op: 0x06, K: 28}})
	}
	if err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}
func probeARP(ctx context.Context, iface *net.Interface, target netip.Addr) (bool, error) {
	if !target.Is4() {
		return false, fmt.Errorf("dhcp: ARP needs IPv4")
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	c, err := openARP(iface)
	if err != nil {
		return false, err
	}
	defer c.Close()
	// RFC5227 probe: sender protocol address zero, target is the candidate.
	b := [28]byte{0, 1, 8, 0, 6, 4, 0, 1}
	copy(b[8:14], iface.HardwareAddr)
	copy(b[24:28], target.AsSlice())
	if err = c.SetWriteDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
		return false, err
	}
	if _, err = c.WriteTo(b[:], &packet.Addr{HardwareAddr: net.HardwareAddr{255, 255, 255, 255, 255, 255}}); err != nil {
		return false, err
	}
	quietUntil := time.Now().Add(200 * time.Millisecond)
	for {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if !time.Now().Before(quietUntil) {
			return false, nil
		}
		deadline := time.Now().Add(20 * time.Millisecond)
		if quietUntil.Before(deadline) {
			deadline = quietUntil
		}
		if err = c.SetReadDeadline(deadline); err != nil {
			return false, err
		}
		var data [64]byte
		n, _, err := c.ReadFrom(data[:])
		if err != nil {
			if e, ok := err.(net.Error); ok && e.Timeout() {
				continue
			}
			return false, err
		}
		if n < 28 || !bytes.Equal(data[:6], []byte{0, 1, 8, 0, 6, 4}) || (data[7] != 1 && data[7] != 2) || data[6] != 0 || bytes.Equal(data[8:14], iface.HardwareAddr) {
			continue
		}
		if bytes.Equal(data[14:18], target.AsSlice()) || (bytes.Equal(data[14:18], []byte{0, 0, 0, 0}) && bytes.Equal(data[24:28], target.AsSlice())) {
			return true, nil
		}
	}
}
