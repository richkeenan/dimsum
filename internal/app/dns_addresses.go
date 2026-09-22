package app

import (
	"cmp"
	"net"
	"net/netip"
	"slices"
	"strings"
)

type dnsInterface struct {
	name      string
	addresses []net.Addr
}

func dnsInterfaces() []dnsInterface {
	interfaces, _ := net.Interfaces()
	var result []dnsInterface
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, err := iface.Addrs()
		if err == nil {
			result = append(result, dnsInterface{name: iface.Name, addresses: addresses})
		}
	}
	return result
}

// Interface names are a best-effort hint, not a reachability guarantee. In
// particular, private IP ranges alone cannot distinguish a LAN from Docker.
func virtualDNSInterface(name string) bool {
	for _, prefix := range []string{"docker", "br-", "veth", "virbr", "cni", "flannel", "podman", "tun", "tap", "utun", "tailscale", "wg"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// clientDNSAddresses expands running wildcard listeners into addresses that
// another device can use. Loopback and scoped link-local addresses are not
// suitable for copying into another device's DNS settings.
func clientDNSAddresses(listeners []string, interfaces []dnsInterface) []string {
	ranks := make(map[netip.Addr]int)
	for _, iface := range interfaces {
		rank := 0
		if virtualDNSInterface(iface.name) {
			rank = 1
		}
		for _, address := range iface.addresses {
			prefix, err := netip.ParsePrefix(address.String())
			if err != nil {
				continue
			}
			ip := prefix.Addr().Unmap()
			if previous, ok := ranks[ip]; !ok || rank < previous {
				ranks[ip] = rank
			}
		}
	}
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
		for candidate := range ranks {
			// Go's "tcp"/"udp" IPv6 wildcard listeners are dual-stack.
			if ip.Is4() && !candidate.Is4() {
				continue
			}
			add(candidate, port)
		}
	}
	slices.SortFunc(addresses, func(a, b string) int {
		left, _ := netip.ParseAddrPort(a)
		right, _ := netip.ParseAddrPort(b)
		if order := cmp.Compare(ranks[left.Addr()], ranks[right.Addr()]); order != 0 {
			return order
		}
		// Addr.Compare orders IPv4 before IPv6, numerically within each family.
		if order := left.Addr().Compare(right.Addr()); order != 0 {
			return order
		}
		return cmp.Compare(left.Port(), right.Port())
	})
	return slices.Compact(addresses)
}
