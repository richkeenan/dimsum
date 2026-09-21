package clients

import (
	"github.com/miekg/dns"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"net"
	"net/netip"
	"testing"
	"time"
)

func mdnsPacket(t *testing.T, records ...dns.RR) []byte {
	t.Helper()
	m := &dns.Msg{MsgHdr: dns.MsgHdr{Response: true, Authoritative: true}, Answer: records}
	m.Compress = true
	p, e := m.Pack()
	require.NoError(t, e)
	return p
}
func rr(t *testing.T, s string) dns.RR {
	t.Helper()
	r, e := dns.NewRR(s)
	require.NoError(t, e)
	return r
}
func TestMDNSAssociationExpiryAndGoodbye(t *testing.T) {
	now := time.Now()
	ip := netip.MustParseAddr("192.0.2.20")
	c := newMDNSCache()
	ptr := rr(t, "20.2.0.192.in-addr.arpa. 120 IN PTR Android.local.")
	a := rr(t, "Android.local. 120 IN A 192.0.2.20")
	require.NoError(t, c.ingest(11, mdnsPacket(t, ptr), now))
	assert.Empty(t, c.lookup(ip, now).Name, "reverse alone cannot identify device")
	require.NoError(t, c.ingest(12, mdnsPacket(t, a), now))
	assert.Empty(t, c.lookup(ip, now).Name, "interfaces cannot be joined")
	c = newMDNSCache()
	require.NoError(t, c.ingest(11, mdnsPacket(t, ptr, a), now))
	assert.Equal(t, "android.local", c.lookup(ip, now).Name)
	require.NoError(t, c.ingest(11, mdnsPacket(t,
		rr(t, "_airplay._tcp.local. 120 IN PTR SONY\\032TV._airplay._tcp.local."),
		rr(t, "SONY\\032TV._airplay._tcp.local. 120 IN SRV 0 0 7000 Android.local."),
		rr(t, "SONY\\032TV._airplay._tcp.local. 120 IN TXT \"model=KD-EXAMPLE\" \"manufacturer=Sony\"")), now))
	got := enrichDiscovered(c.lookup(ip, now), now)
	assert.Equal(t, "SONY TV", got.Name)
	require.NotNil(t, got.Device)
	assert.Equal(t, "tv", got.Device.Category)
	assert.Empty(t, c.lookup(ip, now.Add(121*time.Second)).Name)
	a.Header().Ttl = 0
	require.NoError(t, c.ingest(11, mdnsPacket(t, a), now.Add(time.Second)))
	assert.NotEmpty(t, c.lookup(ip, now.Add(time.Second)).Name)
	assert.Empty(t, c.lookup(ip, now.Add(3*time.Second)).Name)
}
func TestMDNSRejectsWrongAddressMalformedAndAmbiguous(t *testing.T) {
	now := time.Now()
	c := newMDNSCache()
	ip := netip.MustParseAddr("192.0.2.30")
	p := mdnsPacket(t, rr(t, "_home-assistant._tcp.local. 120 IN PTR Home._home-assistant._tcp.local."), rr(t, "Home._home-assistant._tcp.local. 120 IN SRV 0 0 8123 container.local."), rr(t, "container.local. 120 IN A 172.30.32.1"))
	require.NoError(t, c.ingest(11, p, now))
	assert.Empty(t, c.lookup(ip, now).Name)
	for i := 0; i < len(p); i++ {
		assert.Error(t, c.ingest(11, p[:i], now))
	}
	for _, idx := range []int{11, 12} {
		require.NoError(t, c.ingest(idx, mdnsPacket(t, rr(t, "30.2.0.192.in-addr.arpa. 120 IN PTR ha.local."), rr(t, "ha.local. 120 IN A 192.0.2.30")), now))
	}
	assert.Empty(t, c.lookup(ip, now).Name, "ambiguous interfaces must not choose arbitrarily")
}
func TestMDNSIPv6AndFlush(t *testing.T) {
	now := time.Now()
	c := newMDNSCache()
	ip := netip.MustParseAddr("fd00::2")
	rev, _ := dns.ReverseAddr(ip.String())
	a := &dns.AAAA{Hdr: dns.RR_Header{Name: "host.local.", Rrtype: 28, Class: 0x8001, Ttl: 120}, AAAA: net.ParseIP("fd00::2")}
	require.NoError(t, c.ingest(2, mdnsPacket(t, rr(t, rev+" 120 IN PTR host.local."), a), now))
	assert.Equal(t, "host.local", c.lookup(ip, now).Name)
	b := *a
	b.AAAA = net.ParseIP("fd00::3")
	require.NoError(t, c.ingest(2, mdnsPacket(t, &b), now.Add(2*time.Second)))
	assert.NotEmpty(t, c.lookup(ip, now.Add(2*time.Second)).Name)
	assert.Empty(t, c.lookup(ip, now.Add(4*time.Second)).Name)
	assert.Empty(t, c.lookup(netip.MustParseAddr("fe80::2"), now).Name)
}
func FuzzMDNSCache(f *testing.F) {
	f.Add([]byte{0, 0, 132, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	f.Fuzz(func(t *testing.T, p []byte) {
		c := newMDNSCache()
		now := time.Unix(1, 0)
		_ = c.ingest(1, p, now)
		_ = c.lookup(netip.MustParseAddr("192.168.1.2"), now)
	})
}
