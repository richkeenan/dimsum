package dhcp

import (
	"encoding/binary"
	"fmt"
	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/insomniacslk/dhcp/iana"
	"net"
	"net/netip"
)

// DecodeRequest copies only bounded decision/delivery metadata. It deliberately
// validates raw option lengths rather than accepting lossy accessor defaults.
func DecodeRequest(p *dhcpv4.DHCPv4) (Request, error) {
	bad := func() (Request, error) { return Request{}, fmt.Errorf("dhcp: malformed or unsupported request") }
	if p == nil || p.OpCode != dhcpv4.OpcodeBootRequest || p.HWType != iana.HWTypeEthernet || len(p.ClientHWAddr) != 6 || p.HopCount != 0 {
		return bad()
	}
	r := Request{XID: binary.BigEndian.Uint32(p.TransactionID[:]), Broadcast: p.Flags&0x8000 != 0}
	copy(r.MAC[:], p.ClientHWAddr)
	addr := func(ip net.IP) (netip.Addr, bool) {
		if len(ip) == 0 {
			return netip.Addr{}, true
		}
		a, ok := netip.AddrFromSlice(ip)
		return a.Unmap(), ok && a.Unmap().Is4()
	}
	var ok bool
	r.CIAddr, ok = addr(p.ClientIPAddr)
	if !ok {
		return bad()
	}
	r.RelayIP, ok = addr(p.GatewayIPAddr)
	if !ok {
		return bad()
	}
	typ := p.Options[dhcpv4.OptionDHCPMessageType.Code()]
	if len(typ) != 1 {
		return bad()
	}
	r.Type = MessageType(typ[0])
	switch r.Type {
	case Discover, RequestMessage, Decline, Release, Inform:
	default:
		return bad()
	}
	for _, opt := range []struct {
		code dhcpv4.OptionCode
		dst  *netip.Addr
	}{{dhcpv4.OptionRequestedIPAddress, &r.RequestedIP}, {dhcpv4.OptionServerIdentifier, &r.ServerID}} {
		if b, exists := p.Options[opt.code.Code()]; exists {
			if len(b) != 4 {
				return bad()
			}
			*opt.dst = netip.AddrFrom4([4]byte(b))
		}
	}
	if b, exists := p.Options[dhcpv4.OptionClientIdentifier.Code()]; exists {
		if len(b) == 0 || len(b) > 255 {
			return bad()
		}
		r.ClientID = string(b)
	}
	if b := p.Options[dhcpv4.OptionHostName.Code()]; len(b) > 63 {
		return bad()
	} else {
		r.Hostname = string(b)
	}
	if b, exists := p.Options[dhcpv4.OptionMaximumDHCPMessageSize.Code()]; exists {
		if len(b) != 2 {
			return bad()
		}
		r.MaxMessageSize = binary.BigEndian.Uint16(b)
	}
	if b := p.Options[dhcpv4.OptionParameterRequestList.Code()]; len(b) > 255 {
		return bad()
	} else {
		for _, code := range b {
			r.RequestedOptions[code] = true
		}
	}
	if !validRequest(r) {
		return bad()
	}
	return r, nil
}
