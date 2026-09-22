package dhcp

import (
	"encoding/binary"
	"fmt"
	"net"
	"net/netip"

	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/insomniacslk/dhcp/iana"
)

// WireReply owns its payload. MAC is the actual Ethernet destination, including
// direct delivery to a client which has not yet configured its IPv4 address.
type WireReply struct {
	Payload     []byte
	Destination netip.Addr
	MAC         [6]byte
}

func BuildReply(s Settings, r Reply, mtu int) (WireReply, error) {
	var out WireReply
	if r.Type != Offer && r.Type != ACK && r.Type != NAK {
		return out, fmt.Errorf("dhcp: invalid reply type")
	}
	server, err := netip.ParseAddr(s.ServerIP)
	if err != nil || !server.Is4() {
		return out, fmt.Errorf("dhcp: invalid server address")
	}
	prefix, err := netip.ParsePrefix(s.Subnet)
	if err != nil || !prefix.Addr().Is4() {
		return out, fmt.Errorf("dhcp: invalid subnet")
	}
	p := &dhcpv4.DHCPv4{OpCode: dhcpv4.OpcodeBootReply, HWType: iana.HWTypeEthernet, ClientHWAddr: append(net.HardwareAddr(nil), r.Request.MAC[:]...), Options: make(dhcpv4.Options)}
	binary.BigEndian.PutUint32(p.TransactionID[:], r.Request.XID)
	if r.Request.Broadcast {
		p.SetBroadcast()
	}
	if r.Request.CIAddr.Is4() && r.Type != NAK {
		p.ClientIPAddr = net.IP(r.Request.CIAddr.AsSlice())
	}
	if r.Address.Is4() && r.Type != NAK {
		p.YourIPAddr = net.IP(r.Address.AsSlice())
	}
	p.Options[53] = []byte{byte(r.Type)}
	p.Options[54] = server.AsSlice()
	if len(r.Request.ClientID) > 255 {
		return out, fmt.Errorf("dhcp: oversized client identity")
	}
	if r.Request.ClientID != "" {
		p.Options[61] = []byte(r.Request.ClientID)
	}
	if r.Type != NAK {
		if r.Address.Is4() && r.LeaseSeconds > 0 {
			for code, seconds := range map[uint8]int{51: r.LeaseSeconds, 58: r.LeaseSeconds / 2, 59: r.LeaseSeconds * 7 / 8} {
				if code != 51 && r.LeaseSeconds < 3 {
					continue
				}
				b := make([]byte, 4)
				binary.BigEndian.PutUint32(b, uint32(seconds))
				p.Options[code] = b
			}
		}
		if r.Request.RequestedOptions[1] {
			p.Options[1] = []byte(net.CIDRMask(prefix.Bits(), 32))
		}
		if r.Request.RequestedOptions[3] {
			a, e := netip.ParseAddr(s.Gateway)
			if e != nil {
				return out, e
			}
			p.Options[3] = a.AsSlice()
		}
		if r.Request.RequestedOptions[6] {
			p.Options[6] = server.AsSlice()
		}
		if r.Request.RequestedOptions[15] {
			p.Options[15] = []byte(s.LocalDomain)
		}
	}
	limit := 576
	if r.Request.MaxMessageSize >= 576 {
		limit = int(r.Request.MaxMessageSize)
	}
	if limit > MaxDatagram {
		limit = MaxDatagram
	}
	if mtu-28 < limit {
		limit = mtu - 28
	}
	// Mandatory identity/type/server/lease options win over optional configuration.
	out.Payload = p.ToBytes()
	for _, code := range []uint8{15, 3, 6, 1} {
		if len(out.Payload) <= limit {
			break
		}
		delete(p.Options, code)
		out.Payload = p.ToBytes()
	}
	if len(out.Payload) > limit {
		return WireReply{}, fmt.Errorf("dhcp: reply exceeds message size/MTU")
	}
	out.MAC = r.Request.MAC
	switch {
	case r.Type == NAK:
		out.Destination = netip.MustParseAddr("255.255.255.255")
	case r.Request.CIAddr.Is4() && !r.Request.CIAddr.IsUnspecified():
		out.Destination = r.Request.CIAddr
	case r.Request.Broadcast:
		out.Destination = netip.MustParseAddr("255.255.255.255")
	default:
		out.Destination = r.Address
	}
	if !out.Destination.Is4() {
		return WireReply{}, fmt.Errorf("dhcp: no reply destination")
	}
	if out.Destination == netip.MustParseAddr("255.255.255.255") {
		out.MAC = [6]byte{255, 255, 255, 255, 255, 255}
	}
	return out, nil
}
