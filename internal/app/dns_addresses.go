package app

import (
	"net"
	"net/netip"
	"slices"
)

// clientDNSAddresses expands running wildcard listeners into addresses that
// another device can use. Loopback and scoped link-local addresses are not
// suitable for copying into another device's DNS settings.
func clientDNSAddresses(listeners []string, interfaces []net.Addr) []string {
	addresses := make([]string, 0)
	add := func(ip netip.Addr, port string) {
		ip = ip.Unmap()
		if ip.IsGlobalUnicast() && ip.Zone() == "" {
			addresses = append(addresses, net.JoinHostPort(ip.String(), port))
		}
	}
	for _, listener := range listeners {
		host, port, err := net.SplitHostPort(listener)
		if err != nil {
			continue
		}
		ip, err := netip.ParseAddr(host)
		if err != nil {
			continue
		}
		if !ip.IsUnspecified() {
			add(ip, port)
			continue
		}
		for _, iface := range interfaces {
			prefix, err := netip.ParsePrefix(iface.String())
			if err != nil {
				continue
			}
			candidate := prefix.Addr().Unmap()
			// Go's "tcp"/"udp" IPv6 wildcard listeners are dual-stack.
			if ip.Is4() && !candidate.Is4() {
				continue
			}
			add(candidate, port)
		}
	}
	slices.Sort(addresses)
	return slices.Compact(addresses)
}
