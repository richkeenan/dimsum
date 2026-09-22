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
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/insomniacslk/dhcp/dhcpv4"
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
	isolatedLink(t)
	testLinuxDelivery(t)
}

func isolatedLink(t *testing.T) {
	t.Helper()
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
}

func testLinuxDelivery(t *testing.T) {
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

func TestLinuxProductionAdapter(t *testing.T) {
	isolatedLink(t)
	s := fixtureSettings()
	s.Interface = "dhcp-server"
	link, probe, err := OpenSystemLink(s)
	require.NoError(t, err)
	defer link.Close()
	_, _, err = OpenSystemLink(s)
	require.Error(t, err, "second listener must not share production UDP/67")
	clientIF, err := net.InterfaceByName("dhcp-client")
	require.NoError(t, err)
	capture, err := packet.Listen(clientIF, packet.Raw, unix.ETH_P_IP, nil)
	require.NoError(t, err)
	defer capture.Close()
	client, err := nclient4.NewRawUDPConn("dhcp-client", 68)
	require.NoError(t, err)
	defer client.Close()
	_, err = client.WriteTo(discover(), &net.UDPAddr{IP: net.IPv4bcast, Port: 67})
	require.NoError(t, err)
	var buf [8192]byte
	n, peer, truncated, err := link.Receive(buf[:])
	require.NoError(t, err)
	assert.False(t, truncated)
	assert.Equal(t, uint16(68), peer.Port())
	assert.Equal(t, discover(), buf[:n])
	_, err = client.WriteTo(discover(), &net.UDPAddr{IP: net.IPv4bcast, Port: 67})
	require.NoError(t, err)
	_, _, truncated, err = link.Receive(buf[:16])
	require.NoError(t, err)
	assert.True(t, truncated, "UDP adapter must report MSG_TRUNC")
	for _, broadcast := range []bool{false, true} {
		r := Request{MAC: [6]byte{2, 0, 0, 0, 0, 2}, XID: 42, Broadcast: broadcast}
		w, err := BuildReply(s, Reply{Type: Offer, Request: r, Address: netip.MustParseAddr("192.0.2.100"), LeaseSeconds: 3600}, link.MTU())
		require.NoError(t, err)
		require.NoError(t, link.Send(context.Background(), w))
		require.NoError(t, capture.SetReadDeadline(time.Now().Add(time.Second)))
		n, _, err = capture.ReadFrom(buf[:])
		require.NoError(t, err)
		require.Greater(t, n, 42)
		assert.Equal(t, w.MAC[:], buf[:6])
		assert.Equal(t, []byte{192, 0, 2, 2}, buf[26:30])
		assert.Equal(t, w.Destination.AsSlice(), buf[30:34])
		assert.Equal(t, w.Payload, buf[42:n])
	}
	t.Run("quiet-probe", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		conflict, err := probe(ctx, netip.MustParseAddr("192.0.2.101"))
		require.NoError(t, err)
		assert.False(t, conflict)
	})
	t.Run("conflicting-probe", func(t *testing.T) {
		arp, err := packet.Listen(clientIF, packet.Datagram, unix.ETH_P_ARP, nil)
		require.NoError(t, err)
		defer arp.Close()
		done := make(chan error, 1)
		go func() {
			var b [128]byte
			_ = arp.SetReadDeadline(time.Now().Add(time.Second))
			_, _, e := arp.ReadFrom(b[:])
			if e == nil {
				// Independent ARP reply: Ethernet/IPv4, reply, claimant owns .102.
				b = [128]byte{0, 1, 8, 0, 6, 4, 0, 2, 2, 0, 0, 0, 0, 2, 192, 0, 2, 102, 2, 0, 0, 0, 0, 1, 0, 0, 0, 0}
				_, e = arp.WriteTo(b[:28], &packet.Addr{HardwareAddr: net.HardwareAddr{2, 0, 0, 0, 0, 1}})
			}
			done <- e
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		conflict, err := probe(ctx, netip.MustParseAddr("192.0.2.102"))
		require.NoError(t, err)
		assert.True(t, conflict)
		require.NoError(t, <-done)
	})
}

func TestLinuxProductionRuntimeDORA(t *testing.T) {
	isolatedLink(t)
	s := fixtureSettings()
	s.Interface = "dhcp-server"
	link, probe, err := OpenSystemLink(s)
	require.NoError(t, err)
	dir := filepath.Join(t.TempDir(), "dhcp")
	store, rows, err := OpenLeaseStore(dir, 1024, nil)
	require.NoError(t, err)
	rt, err := StartRuntime(context.Background(), s, 1, link, probe, store, rows)
	require.NoError(t, err)
	defer rt.Close(context.Background())
	client, err := nclient4.NewRawUDPConn("dhcp-client", 68)
	require.NoError(t, err)
	defer client.Close()
	require.NoError(t, client.SetDeadline(time.Now().Add(3*time.Second)))
	request := discover()
	request[33] = 2
	_, err = client.WriteTo(request, &net.UDPAddr{IP: net.IPv4bcast, Port: 67})
	require.NoError(t, err)
	var buf [4096]byte
	n, _, err := client.ReadFrom(buf[:])
	require.NoError(t, err)
	offer, err := dhcpv4.FromBytes(buf[:n])
	require.NoError(t, err)
	require.Equal(t, dhcpv4.MessageTypeOffer, offer.MessageType())
	selecting, err := dhcpv4.NewRequestFromOffer(offer)
	require.NoError(t, err)
	_, err = client.WriteTo(selecting.ToBytes(), &net.UDPAddr{IP: net.IPv4bcast, Port: 67})
	require.NoError(t, err)
	n, _, err = client.ReadFrom(buf[:])
	require.NoError(t, err)
	ack, err := dhcpv4.FromBytes(buf[:n])
	require.NoError(t, err)
	assert.Equal(t, dhcpv4.MessageTypeAck, ack.MessageType())
	assert.Equal(t, "192.0.2.100", ack.YourIPAddr.String())
	require.NoError(t, rt.Close(context.Background()))
	recovered, rows, err := OpenLeaseStore(dir, 1024, nil)
	require.NoError(t, err)
	defer recovered.Close(context.Background())
	require.Len(t, rows.Leases, 1)
	assert.Equal(t, "192.0.2.100", rows.Leases[0].Address.String())
}

func TestLinuxRejectsNonPermanentAddress(t *testing.T) {
	isolatedLink(t)
	out, err := exec.Command("ip", "addr", "change", "192.0.2.2/24", "dev", "dhcp-server", "valid_lft", "300", "preferred_lft", "300").CombinedOutput()
	require.NoError(t, err, "%s", out)
	s := fixtureSettings()
	s.Interface = "dhcp-server"
	link, _, err := OpenSystemLink(s)
	if link != nil {
		link.Close()
	}
	require.Error(t, err)
}

func TestLinuxIndependentUDHCPC(t *testing.T) {
	if os.Getenv("DIMSUM_DHCP_UDHCPC_TEST") != "1" {
		t.Skip("requires isolated fixture image with busybox udhcpc")
	}
	for _, broadcast := range []bool{false, true} {
		t.Run(fmt.Sprintf("broadcast-%t", broadcast), func(t *testing.T) {
			isolatedLink(t)
			settings := fixtureSettings()
			settings.Interface = "dhcp-server"
			link, probe, err := OpenSystemLink(settings)
			require.NoError(t, err)
			dir := t.TempDir()
			store, rows, err := OpenLeaseStore(filepath.Join(dir, "dhcp"), 1024, nil)
			require.NoError(t, err)
			rt, err := StartRuntime(context.Background(), settings, 1, link, probe, store, rows)
			require.NoError(t, err)
			defer rt.Close(context.Background())
			script := filepath.Join(dir, "client-script")
			require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\nif [ \"$1\" = bound ]; then printf '%s\\n' \"$ip\" \"$serverid\" \"$dns\" \"$router\" \"$subnet\" \"$lease\" > \"$DIMSUM_CLIENT_RESULT\"; fi\n"), 0700))
			args := []string{"udhcpc", "-f", "-n", "-q", "-t", "3", "-T", "1", "-i", "dhcp-client", "-s", script}
			if broadcast {
				args = append(args, "-B")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "busybox", args...)
			result := filepath.Join(dir, "result")
			cmd.Env = append(os.Environ(), "DIMSUM_CLIENT_RESULT="+result)
			output, err := cmd.CombinedOutput()
			require.NoError(t, err, "%s", output)
			t.Logf("%s", output)
			values, err := os.ReadFile(result)
			require.NoError(t, err)
			fields := strings.Fields(string(values))
			require.Len(t, fields, 6)
			assert.Equal(t, []string{"192.0.2.100", "192.0.2.2", "192.0.2.2", "192.0.2.1", "255.255.255.0"}, fields[:5])
			assert.NotEqual(t, "0", fields[5])
			require.NoError(t, rt.Close(context.Background()))
			recovered, rows, err := OpenLeaseStore(filepath.Join(dir, "dhcp"), 1024, nil)
			require.NoError(t, err)
			defer recovered.Close(context.Background())
			require.Len(t, rows.Leases, 1)
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
