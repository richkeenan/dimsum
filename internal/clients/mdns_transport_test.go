package clients

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/ipv4"
)

func TestMDNSAutoSelectsConnectedLANInterfaces(t *testing.T) {
	connected := net.FlagUp | net.FlagRunning | net.FlagMulticast
	for _, tt := range []struct {
		name           string
		flags          net.Flags
		explicit, want bool
	}{
		{"eth0", connected, false, true},
		{"wlan0", connected, false, true},
		{"en0", connected, false, true},
		{"eth0", net.FlagUp | net.FlagMulticast, false, false},
		{"lo", connected | net.FlagLoopback, true, false},
		{"tailscale0", connected | net.FlagPointToPoint, false, false},
		{"docker0", connected, false, false},
		{"br-example", connected, false, false},
		{"veth-example", connected, false, false},
		{"utun0", connected | net.FlagPointToPoint, false, false},
		{"docker0", connected, true, true},
		{"br-example", connected, true, true},
		{"wlan0", net.FlagMulticast, true, false},
	} {
		assert.Equal(t, tt.want, eligibleMDNSInterface(net.Interface{Name: tt.name, Flags: tt.flags}, tt.explicit), "%s explicit=%v", tt.name, tt.explicit)
	}
}

func TestMDNSTerminalReadErrorSurvivesFullPacketQueue(t *testing.T) {
	conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	transport := &multicastTransport{v4: ipv4.NewPacketConn(conn), packets: make(chan mdnsDatagram, 1), cancel: cancel}
	transport.packets <- mdnsDatagram{wire: []byte("queued packet")}
	require.NoError(t, conn.Close()) // A real terminal read failure, no LAN traffic.
	done := make(chan struct{})
	go func() { transport.read(ctx, false); close(done) }()
	select {
	case <-done:
		t.Error("reader exited before it could deliver the terminal error")
	case <-time.After(50 * time.Millisecond):
	}
	<-transport.packets
	select {
	case packet := <-transport.packets:
		assert.Error(t, packet.err)
	case <-time.After(time.Second):
		t.Error("terminal error was lost behind ordinary packets")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reader failed to stop")
	}
}
