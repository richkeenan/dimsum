package app

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClientDNSAddresses(t *testing.T) {
	var interfaces []net.Addr
	for _, cidr := range []string{"127.0.0.1/8", "192.0.2.53/24", "2001:db8::53/64", "fe80::1/64", "::1/128"} {
		ip, network, err := net.ParseCIDR(cidr)
		require.NoError(t, err)
		network.IP = ip
		interfaces = append(interfaces, network)
	}
	for _, tt := range []struct {
		name            string
		listeners, want []string
	}{
		{"explicit bindings keep their port", []string{"192.0.2.53:5353", "[2001:db8::53]:53"}, []string{"192.0.2.53:5353", "[2001:db8::53]:53"}},
		{"IPv4 wildcard excludes local and IPv6 addresses", []string{"0.0.0.0:53"}, []string{"192.0.2.53:53"}},
		{"dual stack wildcard expands and deduplicates", []string{"[::]:53", "192.0.2.53:53"}, []string{"192.0.2.53:53", "[2001:db8::53]:53"}},
		{"loopback is not advertised to clients", []string{"127.0.0.1:53", "[::1]:53"}, []string{}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, clientDNSAddresses(tt.listeners, interfaces))
		})
	}
}
