//go:build unix

package app

import (
	"fmt"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/richkeenan/dimsum/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLifecycleSuppliesVerifiedDualStackEndpointsToDHCP(t *testing.T) {
	c := config.Default()
	c.DNS.Listen = []string{"[::]:0"}
	c.DNS.Upstreams = []string{"127.0.0.1:9"}
	c.Admin.Listen = "127.0.0.1:0"
	s := new(Service)
	require.NoError(t, s.StartForwarding(t.Context(), c))
	t.Cleanup(func() { _ = s.Close() })
	require.True(t, s.Ready())
	bound, err := netip.ParseAddrPort(s.Addresses().DNS[0])
	require.NoError(t, err)
	requireDualStackSupport(t, int(bound.Port()))
	require.NotNil(t, s.dhcp)
	require.Len(t, s.dhcp.dns, 1)
	a, err := netip.ParseAddrPort(s.dhcp.dns[0])
	require.NoError(t, err)
	assert.Equal(t, netip.IPv4Unspecified(), a.Addr())
	assert.NotZero(t, a.Port())
	assert.Contains(t, s.Addresses().DNS[0], "[::]", "public listener metadata stays unchanged")
}

func TestDHCPDNSRequiresIPv4OnBothTransports(t *testing.T) {
	for _, tc := range []struct {
		name, tcp, udp, host string
		want                 bool
	}{
		{"IPv4", "tcp4", "udp4", "0.0.0.0", true},
		{"dual stack", "tcp", "udp", "::", true},
		{"IPv6 only", "tcp6", "udp6", "::", false},
		{"UDP IPv6 only", "tcp", "udp6", "::", false},
		{"TCP IPv6 only", "tcp6", "udp", "::", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tcp, err := net.Listen(tc.tcp, net.JoinHostPort(tc.host, "0"))
			require.NoError(t, err)
			t.Cleanup(func() { _ = tcp.Close() })
			if tc.want && tc.tcp == "tcp" {
				requireDualStackSupport(t, tcp.Addr().(*net.TCPAddr).Port)
			}
			udp, err := net.ListenPacket(tc.udp, tcp.Addr().String())
			require.NoError(t, err)
			t.Cleanup(func() { _ = udp.Close() })
			got := dhcpIPv4Listeners([]net.Listener{tcp}, []net.PacketConn{udp})
			if !tc.want {
				assert.Empty(t, got, "a wildcard address alone is not proof of IPv4 support")
				return
			}
			port := tcp.Addr().(*net.TCPAddr).Port
			require.Equal(t, []string{fmt.Sprintf("0.0.0.0:%d", port)}, got)
			endpoint := fmt.Sprintf("127.0.0.1:%d", port)
			client, err := net.DialTimeout("tcp4", endpoint, time.Second)
			require.NoError(t, err)
			_ = client.Close()
			u, err := net.DialTimeout("udp4", endpoint, time.Second)
			require.NoError(t, err)
			defer u.Close()
			_, err = u.Write([]byte("ipv4"))
			require.NoError(t, err)
			require.NoError(t, udp.SetReadDeadline(time.Now().Add(time.Second)))
			var b [16]byte
			n, _, err := udp.ReadFrom(b[:])
			require.NoError(t, err)
			assert.Equal(t, "ipv4", string(b[:n]))
		})
	}
}

func requireDualStackSupport(t *testing.T, port int) {
	t.Helper()
	c, err := net.DialTimeout("tcp4", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
	if err != nil {
		t.Skipf("host does not support IPv4 on an IPv6 wildcard: %v", err)
	}
	require.NoError(t, c.Close())
}

func TestDHCPDNSDoesNotReportClosedOrUnpairedSockets(t *testing.T) {
	tcp, err := net.Listen("tcp", "[::]:0")
	require.NoError(t, err)
	defer tcp.Close()
	udp, err := net.ListenPacket("udp", tcp.Addr().String())
	require.NoError(t, err)
	assert.Empty(t, dhcpIPv4Listeners([]net.Listener{tcp}, nil))
	require.NoError(t, udp.Close())
	assert.Empty(t, dhcpIPv4Listeners([]net.Listener{tcp}, []net.PacketConn{udp}))
}

func TestDHCPDNSKeepsSpecificIPv4Address(t *testing.T) {
	tcp, err := net.Listen("tcp4", "127.0.0.1:0")
	require.NoError(t, err)
	defer tcp.Close()
	udp, err := net.ListenPacket("udp4", tcp.Addr().String())
	require.NoError(t, err)
	defer udp.Close()
	got := dhcpIPv4Listeners([]net.Listener{tcp}, []net.PacketConn{udp})
	require.Len(t, got, 1)
	a, err := netip.ParseAddrPort(got[0])
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1", a.Addr().String(), "loopback must not become a wildcard")
}
