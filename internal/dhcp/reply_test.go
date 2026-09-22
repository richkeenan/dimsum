package dhcp

import (
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReplyWireIdentityOptionsAndDelivery(t *testing.T) {
	s := fixtureSettings()
	r := client(1, RequestMessage)
	r.XID = 0x12345678
	r.ClientID = "\x01opaque"
	r.MaxMessageSize = 576
	for _, code := range []int{1, 3, 6, 15} {
		r.RequestedOptions[code] = true
	}
	for _, tc := range []struct {
		name      string
		kind      MessageType
		ci        string
		broadcast bool
		dst       string
		lease     bool
	}{
		{"offer", Offer, "", false, "192.0.2.100", true},
		{"broadcast", ACK, "", true, "255.255.255.255", true},
		{"renew", ACK, "192.0.2.100", false, "192.0.2.100", true},
		{"nak", NAK, "192.0.2.100", false, "255.255.255.255", false},
		{"inform", ACK, "192.0.2.100", false, "192.0.2.100", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := r
			req.Broadcast = tc.broadcast
			if tc.ci != "" {
				req.CIAddr = netip.MustParseAddr(tc.ci)
			}
			addr := netip.MustParseAddr("192.0.2.100")
			seconds := 3600
			if !tc.lease {
				addr = netip.Addr{}
				seconds = 0
			}
			wire, err := BuildReply(s, Reply{Type: tc.kind, Request: req, Address: addr, LeaseSeconds: seconds}, 1500)
			require.NoError(t, err)
			assert.LessOrEqual(t, len(wire.Payload), 576)
			assert.Equal(t, tc.dst, wire.Destination.String())
			p, err := dhcpv4.FromBytes(wire.Payload)
			require.NoError(t, err)
			assert.Equal(t, dhcpv4.TransactionID{0x12, 0x34, 0x56, 0x78}, p.TransactionID)
			assert.Equal(t, net.HardwareAddr(r.MAC[:]), p.ClientHWAddr)
			assert.Equal(t, []byte(r.ClientID), p.GetOneOption(dhcpv4.OptionClientIdentifier))
			assert.True(t, p.ServerIdentifier().Equal(net.ParseIP("192.0.2.2")))
			assert.EqualValues(t, tc.kind, p.MessageType())
			if tc.kind != NAK {
				assert.Equal(t, []net.IP{net.IP{192, 0, 2, 2}}, p.DNS())
				assert.Equal(t, s.LocalDomain, p.DomainName())
			}
			if tc.lease {
				assert.Equal(t, time.Hour, p.IPAddressLeaseTime(0))
				assert.Equal(t, 30*time.Minute, p.IPAddressRenewalTime(0))
				assert.Equal(t, 3150*time.Second, p.IPAddressRebindingTime(0))
			} else {
				assert.Nil(t, p.GetOneOption(dhcpv4.OptionIPAddressLeaseTime))
			}
			if tc.dst == "255.255.255.255" {
				assert.Equal(t, [6]byte{255, 255, 255, 255, 255, 255}, wire.MAC)
			} else {
				assert.Equal(t, r.MAC, wire.MAC)
			}
		})
	}
}

func TestReplyBoundAndRequestedOptions(t *testing.T) {
	r := client(1, Inform)
	r.CIAddr = netip.MustParseAddr("192.0.2.100")
	wire, err := BuildReply(fixtureSettings(), Reply{Type: ACK, Request: r}, 1500)
	require.NoError(t, err)
	p, err := dhcpv4.FromBytes(wire.Payload)
	require.NoError(t, err)
	assert.Empty(t, p.DomainName())
	_, err = BuildReply(fixtureSettings(), Reply{Type: ACK, Request: r}, 280)
	assert.Error(t, err)
}

func TestReplyTinyRemainingGrantOmitsInvalidTimers(t *testing.T) {
	r := client(1, RequestMessage)
	wire, err := BuildReply(fixtureSettings(), Reply{Type: ACK, Request: r, Address: netip.MustParseAddr("192.0.2.100"), LeaseSeconds: 1}, 1500)
	require.NoError(t, err)
	p, err := dhcpv4.FromBytes(wire.Payload)
	require.NoError(t, err)
	assert.Equal(t, time.Second, p.IPAddressLeaseTime(0))
	assert.Nil(t, p.GetOneOption(dhcpv4.OptionRenewTimeValue))
	assert.Nil(t, p.GetOneOption(dhcpv4.OptionRebindingTimeValue))
}
