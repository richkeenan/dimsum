package app

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClientDNSAddresses(t *testing.T) {
	var addresses []net.Addr
	for _, cidr := range []string{"127.0.0.1/8", "192.0.2.53/24", "2001:db8::53/64", "fe80::1/64", "::1/128"} {
		ip, network, err := net.ParseCIDR(cidr)
		require.NoError(t, err)
		network.IP = ip
		addresses = append(addresses, network)
	}
	interfaces := []dnsInterface{{name: "eth0", addresses: addresses}}
	for _, tt := range []struct {
		name            string
		listeners, want []string
	}{
		{"explicit bindings keep their port", []string{"192.0.2.53:5353", "[2001:db8::53]:53"}, []string{"192.0.2.53:5353", "[2001:db8::53]:53"}},
		{"IPv4 wildcard excludes local and IPv6 addresses", []string{"0.0.0.0:53"}, []string{"192.0.2.53:53"}},
		{"dual stack wildcard expands and deduplicates", []string{"[::]:53", "192.0.2.53:53"}, []string{"192.0.2.53:53", "[2001:db8::53]:53"}},
		{"loopback is not advertised to clients", []string{"127.0.0.1:53", "[::1]:53"}, []string{}},
		{"addresses sort numerically within a family", []string{"192.0.2.100:53", "192.0.2.9:53"}, []string{"192.0.2.9:53", "192.0.2.100:53"}},
		{"IPv6 addresses sort numerically", []string{"[2001:db8::10]:53", "[2001:db8::2]:53"}, []string{"[2001:db8::2]:53", "[2001:db8::10]:53"}},
		{"ports sort numerically for the same address", []string{"192.0.2.53:1053", "192.0.2.53:53"}, []string{"192.0.2.53:53", "192.0.2.53:1053"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, clientDNSAddresses(tt.listeners, interfaces))
		})
	}
}

func TestClientDNSAddressesPreferLANInterfaces(t *testing.T) {
	var interfaces []dnsInterface
	for _, iface := range []struct{ name, cidr string }{
		{"docker0", "192.0.2.1/24"},
		{"br-0123456789ab", "192.0.2.2/24"},
		{"eth0", "198.51.100.100/24"},
		{"wlan0", "198.51.100.9/24"},
		{"eth0", "2001:db8::53/64"},
		{"veth1234", "198.51.100.9/24"}, // Duplicate on a virtual interface must not demote the LAN address.
		{"utun0", "2001:db8:1::1/64"},
	} {
		ip, network, err := net.ParseCIDR(iface.cidr)
		require.NoError(t, err)
		network.IP = ip
		interfaces = append(interfaces, dnsInterface{name: iface.name, addresses: []net.Addr{network}})
	}
	want := []string{"198.51.100.9:53", "198.51.100.100:53", "[2001:db8::53]:53", "192.0.2.1:53", "192.0.2.2:53", "[2001:db8:1::1]:53"}
	assert.Equal(t, want, clientDNSAddresses([]string{"[::]:53", "192.0.2.1:53"}, interfaces))
	assert.Equal(t, []string{"198.51.100.100:53", "192.0.2.1:53"}, clientDNSAddresses([]string{"192.0.2.1:53", "198.51.100.100:53"}, interfaces))
}
