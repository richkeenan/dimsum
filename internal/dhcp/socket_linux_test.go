//go:build linux

package dhcp

import (
	"bytes"
	"encoding/binary"
	"net"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/insomniacslk/dhcp/dhcpv4/nclient4"
	"github.com/insomniacslk/dhcp/dhcpv4/server4"
	"github.com/mdlayher/packet"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// This opt-in socket-strategy experiment requires an isolated Linux network
// namespace, iproute2, CAP_NET_ADMIN (fixture), CAP_NET_RAW and normally
// CAP_NET_BIND_SERVICE (service). Never opt in on a production network.
// It deliberately does not supply an app-facing socket implementation yet.
func TestLinuxIsolatedDelivery(t *testing.T) {
	if os.Getenv("DIMSUM_DHCP_ISOLATED_TEST") != "1" {
		t.Skip("requires isolated Linux namespace; see test comment")
	}
	run := func(args ...string) {
		t.Helper()
		out, err := exec.Command("ip", args...).CombinedOutput()
		require.NoError(t, err, "ip %v: %s", args, out)
	}
	run("link", "add", "dhcp-server", "type", "veth", "peer", "name", "dhcp-client")
	t.Cleanup(func() {
		out, err := exec.Command("ip", "link", "del", "dhcp-server").CombinedOutput()
		assert.NoError(t, err, "%s", out)
	})
	run("link", "set", "dhcp-server", "address", "02:00:00:00:00:01")
	run("link", "set", "dhcp-client", "address", "02:00:00:00:00:02")
	run("addr", "add", "192.0.2.2/24", "dev", "dhcp-server")
	run("link", "set", "dhcp-server", "up")
	run("link", "set", "dhcp-client", "up")
	clientIF, err := net.InterfaceByName("dhcp-client")
	require.NoError(t, err)
	addrs, err := clientIF.Addrs()
	require.NoError(t, err)
	for _, a := range addrs {
		ip, _, err := net.ParseCIDR(a.String())
		require.NoError(t, err)
		require.Nil(t, ip.To4(), "client must have no IPv4 configuration")
	}
	server, err := server4.NewIPv4UDPConn("dhcp-server", &net.UDPAddr{Port: 67})
	require.NoError(t, err)
	defer server.Close()
	require.NoError(t, server.SetDeadline(time.Now().Add(3*time.Second)))
	client, err := nclient4.NewRawUDPConn("dhcp-client", 68)
	require.NoError(t, err)
	defer client.Close()
	_, err = client.WriteTo(discover(), &net.UDPAddr{IP: net.IPv4bcast, Port: 67})
	require.NoError(t, err)
	var buf [65536]byte
	n, peer, err := server.ReadFromUDP(buf[:])
	require.NoError(t, err)
	assert.Equal(t, discover(), buf[:n])
	assert.Equal(t, 68, peer.Port)
	assert.True(t, peer.IP.Equal(net.IPv4zero))

	// AF_PACKET SOCK_DGRAM selects the interface and Ethernet destination directly,
	// bypassing neighbor resolution for yiaddr before the client configures it.
	capture, err := packet.Listen(clientIF, packet.Raw, unix.ETH_P_IP, nil)
	require.NoError(t, err)
	defer capture.Close()
	require.NoError(t, capture.SetReadDeadline(time.Now().Add(3*time.Second)))
	serverIF, err := net.InterfaceByName("dhcp-server")
	require.NoError(t, err)
	sender, err := packet.Listen(serverIF, packet.Datagram, unix.ETH_P_IP, nil)
	require.NoError(t, err)
	defer sender.Close()
	for _, tc := range []struct {
		name string
		ip   net.IP
		mac  net.HardwareAddr
	}{
		{"broadcast", net.IPv4bcast, net.HardwareAddr{255, 255, 255, 255, 255, 255}},
		{"unicast-before-address", net.IPv4(192, 0, 2, 100), clientIF.HardwareAddr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wire := ipv4UDPFixture(discover(), net.IPv4(192, 0, 2, 2), tc.ip)
			_, err := sender.WriteTo(wire, &packet.Addr{HardwareAddr: tc.mac})
			require.NoError(t, err)
			n, _, err := capture.ReadFrom(buf[:])
			require.NoError(t, err)
			require.GreaterOrEqual(t, n, 42)
			assert.Equal(t, []byte(tc.mac), buf[:6])
			assert.Equal(t, []byte(serverIF.HardwareAddr), buf[6:12])
			assert.Equal(t, []byte{192, 0, 2, 2}, buf[26:30])
			assert.Equal(t, []byte(tc.ip.To4()), buf[30:34])
			assert.EqualValues(t, 67, binary.BigEndian.Uint16(buf[34:36]))
			assert.EqualValues(t, 68, binary.BigEndian.Uint16(buf[36:38]))
			assert.True(t, bytes.Equal(discover(), buf[42:n]))
		})
	}
}

// Test-only candidate encoder: no fragmentation, UDP checksum omitted as allowed
// by IPv4. A production sender must enforce MTU and reply size and deadlines.
func ipv4UDPFixture(payload []byte, src, dst net.IP) []byte {
	b := make([]byte, 28+len(payload))
	b[0] = 0x45
	b[8] = 64
	b[9] = 17
	binary.BigEndian.PutUint16(b[2:4], uint16(len(b)))
	copy(b[12:16], src.To4())
	copy(b[16:20], dst.To4())
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
	binary.BigEndian.PutUint16(b[24:26], uint16(8+len(payload)))
	copy(b[28:], payload)
	return b
}
