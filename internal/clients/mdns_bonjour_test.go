package clients

import (
	"encoding/binary"
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A complete synthetic Bonjour response. Its SRV/NSEC RDATA uses real mDNS
// compression rather than the unicast encoding produced by DNS test libraries.
func bonjourPacket() []byte {
	p := []byte{0, 0, 0x84, 0, 0, 0, 0, 2, 0, 0, 0, 3}
	ptr := func(offset int) []byte { return []byte{0xc0 | byte(offset>>8), byte(offset)} }
	add := func(owner []byte, kind uint16, data []byte) int {
		p = append(p, owner...)
		p = binary.BigEndian.AppendUint16(p, kind)
		class := uint16(0x8001)
		if kind == 12 {
			class = 1
		}
		p = binary.BigEndian.AppendUint16(p, class)
		p = binary.BigEndian.AppendUint32(p, 120)
		p = binary.BigEndian.AppendUint16(p, uint16(len(data)))
		offset := len(p)
		p = append(p, data...)
		return offset
	}
	add([]byte("\x06none-2\x05local\x00"), 1, []byte{192, 0, 2, 20})
	service := len(p)
	instance := add([]byte("\x08_airplay\x04_tcp\x05local\x00"), 12, append([]byte("\x0aExample TV"), ptr(service)...))
	add(ptr(instance), 33, append([]byte{0, 0, 0, 0, 0x1b, 0x58}, ptr(12)...))
	add(ptr(instance), 16, []byte("\x10model=KD-EXAMPLE\x11manufacturer=Sony"))
	add(ptr(12), 47, append(ptr(12), 0, 1, 0x40))
	return p
}

func TestBonjourCompressedResponseAssociatesService(t *testing.T) {
	now := time.Now()
	c := newMDNSCache()
	require.NoError(t, c.ingest(2, bonjourPacket(), now))
	n := c.lookup(netip.MustParseAddr("192.0.2.20"), now)
	assert.Equal(t, "none-2.local", n.Name)
	require.NotNil(t, n.Device)
	require.Len(t, n.Device.Evidence, 1)
	assert.Equal(t, "Example TV", n.Device.Evidence[0].Label)
	assert.Equal(t, "KD-EXAMPLE", n.Device.Evidence[0].Model)
	assert.Equal(t, "tv", enrichDiscovered(n, now).Device.Category)
	assert.Empty(t, c.lookup(netip.MustParseAddr("192.0.2.21"), now).Name)
	assert.Empty(t, c.lookup(n.Address, now.Add(121*time.Second)).Name)
}

func TestBonjourOpaqueInstanceUsesAdvertisedFriendlyName(t *testing.T) {
	now := time.Now()
	c := newMDNSCache()
	require.NoError(t, c.ingest(2, mdnsPacket(t,
		rr(t, "_googlecast._tcp.local. 120 IN PTR 0123456789abcdef._googlecast._tcp.local."),
		rr(t, "0123456789abcdef._googlecast._tcp.local. 120 IN SRV 0 0 8009 Android.local."),
		rr(t, "0123456789abcdef._googlecast._tcp.local. 120 IN TXT \"fn=Example Display\""),
		rr(t, "Android.local. 120 IN A 192.0.2.20")), now))
	assert.Equal(t, "Example Display", enrichDiscovered(c.lookup(netip.MustParseAddr("192.0.2.20"), now), now).Name)
}

func TestExpiredMetadataDoesNotHideLiveHostname(t *testing.T) {
	now := time.Now()
	ip := netip.MustParseAddr("192.0.2.20")
	c := newMDNSCache()
	require.NoError(t, c.ingest(2, mdnsPacket(t,
		rr(t, "20.2.0.192.in-addr.arpa. 120 IN PTR Android.local."),
		rr(t, "Android.local. 120 IN A 192.0.2.20"),
		rr(t, "_googlecast._tcp.local. 120 IN PTR 0123456789abcdef._googlecast._tcp.local."),
		rr(t, "0123456789abcdef._googlecast._tcp.local. 120 IN SRV 0 0 8009 Android.local."),
		rr(t, "0123456789abcdef._googlecast._tcp.local. 1 IN TXT \"fn=Example Display\" \"device_type=tv\"")), now))
	snapshot := enrichDiscovered(c.lookup(ip, now), now)
	assert.Equal(t, "Example Display", snapshot.Name)
	assert.Equal(t, "tv", snapshot.Device.Category)
	// A read between worker publications must re-evaluate expired evidence.
	after := mergeDiscovered(Name{Address: ip, Source: "unknown"}, snapshot, now.Add(2*time.Second))
	assert.Equal(t, "android.local", after.Name)
	assert.Equal(t, "unknown", after.Device.Category)
	assert.True(t, after.Fresh)
	assert.Empty(t, mergeDiscovered(Name{Address: ip}, snapshot, now.Add(121*time.Second)).Name)
}

func TestConflictingTXTDoesNotChooseAFriendlyName(t *testing.T) {
	now := time.Now()
	c := newMDNSCache()
	require.NoError(t, c.ingest(2, mdnsPacket(t,
		rr(t, "_googlecast._tcp.local. 120 IN PTR 0123456789abcdef._googlecast._tcp.local."),
		rr(t, "0123456789abcdef._googlecast._tcp.local. 120 IN SRV 0 0 8009 Android.local."),
		rr(t, "0123456789abcdef._googlecast._tcp.local. 120 IN TXT \"fn=Example One\""),
		rr(t, "0123456789abcdef._googlecast._tcp.local. 120 IN TXT \"fn=Example Two\""),
		rr(t, "Android.local. 120 IN A 192.0.2.20")), now))
	assert.Equal(t, "android.local", enrichDiscovered(c.lookup(netip.MustParseAddr("192.0.2.20"), now), now).Name)
}

func FuzzBonjourResponse(f *testing.F) {
	f.Add(bonjourPacket())
	f.Fuzz(func(t *testing.T, packet []byte) {
		c := newMDNSCache()
		now := time.Unix(1, 0)
		_ = c.ingest(2, packet, now)
		_ = enrichDiscovered(c.lookup(netip.MustParseAddr("192.0.2.20"), now), now)
		c.expire(now.Add(time.Hour))
		assert.Empty(t, c.records)
	})
}
