package dhcp

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type probeSocketFixture struct {
	packets       [][]byte
	reads, writes int
	sent          []byte
}

func (*probeSocketFixture) SetDeadline(time.Time) error { return nil }
func (s *probeSocketFixture) WriteToUDPAddrPort(b []byte, _ netip.AddrPort) (int, error) {
	s.writes++
	s.sent = append([]byte{}, b...)
	return len(b), nil
}
func (s *probeSocketFixture) ReadFromUDPAddrPort(b []byte) (int, netip.AddrPort, error) {
	p := s.packets[s.reads%len(s.packets)]
	s.reads++
	return copy(b, p), netip.MustParseAddrPort("192.0.2.1:67"), nil
}

func TestOtherServerDiagnosticBoundedAndMatched(t *testing.T) {
	discovery, e := dhcpv4.NewDiscovery([]byte{2, 0, 0, 0, 0, 1}, dhcpv4.WithBroadcast(true))
	require.NoError(t, e)
	offer, e := dhcpv4.NewReplyFromRequest(discovery, dhcpv4.WithMessageType(dhcpv4.MessageTypeOffer), dhcpv4.WithOption(dhcpv4.OptServerIdentifier([]byte{192, 0, 2, 1})))
	require.NoError(t, e)
	own, e := dhcpv4.NewReplyFromRequest(discovery, dhcpv4.WithMessageType(dhcpv4.MessageTypeOffer), dhcpv4.WithOption(dhcpv4.OptServerIdentifier([]byte{192, 0, 2, 2})))
	require.NoError(t, e)
	spoof := offer.ToBytes()
	spoof[4] ^= 0xff
	sock := &probeSocketFixture{packets: [][]byte{offer.ToBytes(), own.ToBytes(), spoof, make([]byte, MaxDatagram+1), {0}}}
	result, e := observeServers(t.Context(), sock, discovery, "192.0.2.2", time.Second)
	require.NoError(t, e)
	assert.Equal(t, []string{"192.0.2.1"}, result.Servers)
	assert.True(t, result.Truncated)
	assert.Equal(t, 64, sock.reads)
	assert.Equal(t, 1, sock.writes)
	sent, e := dhcpv4.FromBytes(sock.sent)
	require.NoError(t, e)
	assert.Equal(t, dhcpv4.MessageTypeDiscover, sent.MessageType())
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, e = observeServers(ctx, sock, discovery, "192.0.2.2", time.Second)
	assert.ErrorIs(t, e, context.Canceled)
	assert.Equal(t, 1, sock.writes)
	_, e = ProbeServers(t.Context(), Settings{}, 4*time.Second)
	assert.Error(t, e, "reject before opening sockets")
}
