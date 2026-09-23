package clients

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiscoveryPublicationRetainsSleepingClientAndAcceptsReplacement(t *testing.T) {
	address := netip.MustParseAddr("192.0.2.20")
	view, err := NewView(Settings{MDNS: MDNSSettings{Enabled: true}}, nil, nil)
	require.NoError(t, err)
	m := New(func() *View { return view })
	f := &fakeMDNS{packets: make(chan mdnsDatagram, 4), sent: make(chan []byte, 16)}
	m.openMDNS = func(context.Context, MDNSSettings) (mdnsTransport, []string) { return f, nil }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); m.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	m.mdnsObserve <- address
	f.packets <- mdnsDatagram{iface: 1, wire: mdnsPacket(t,
		rr(t, "20.2.0.192.in-addr.arpa. 2 IN PTR Example-iPhone.local."),
		rr(t, "Example-iPhone.local. 2 IN A 192.0.2.20"))}
	require.Eventually(t, func() bool { n := m.Get(address); return n.Name == "example-iphone.local" && n.Fresh }, 3*time.Second, 10*time.Millisecond)
	require.Eventually(t, func() bool { n := m.Get(address); return n.Name == "example-iphone.local" && !n.Fresh }, 4*time.Second, 10*time.Millisecond)
	// Wait for a publication after the wire records have expired, not merely a stale read.
	require.Eventually(t, func() bool { return m.Diagnostics().Records == 0 }, 3*time.Second, 10*time.Millisecond)
	assert.Equal(t, "example-iphone.local", m.Get(address).Name)
	f.packets <- mdnsDatagram{iface: 1, wire: mdnsPacket(t,
		rr(t, "20.2.0.192.in-addr.arpa. 120 IN PTR Replacement-MacBook.local."),
		rr(t, "Replacement-MacBook.local. 120 IN A 192.0.2.20"))}
	require.Eventually(t, func() bool {
		n := m.Get(address)
		return n.Name == "replacement-macbook.local" && n.Fresh && n.Device.Category == "laptop"
	}, 3*time.Second, 10*time.Millisecond)
}

func TestSleepingClientRetainsIdentityAheadOfDNSGuess(t *testing.T) {
	now := time.Now()
	address := netip.MustParseAddr("192.0.2.20")
	view, err := NewView(Settings{}, nil, nil)
	require.NoError(t, err)
	m := New(func() *View { return view })
	m.mdnsNames[address] = entry{view, Name{
		Address: address, Name: "work-macbook.local", Source: "mdns",
		Updated: now.Add(-47 * time.Hour), Expires: now.Add(-47*time.Hour + 5*time.Minute), Fresh: true,
		Device: &Enrichment{Hostname: "work-macbook.local", Category: "laptop", Fresh: true, Evidence: []Evidence{}},
	}}
	m.ReplaceDNSActivity([]DNSActivity{{Address: address, Domain: "example-ats.iot.us-west-2.amazonaws.com", First: now.Add(-time.Minute), Last: now, Corroborated: now.Add(-time.Minute), Count: 2}})
	for range 2 {
		got := m.Get(address)
		assert.Equal(t, "work-macbook.local", got.Name)
		assert.Equal(t, "mdns", got.Source)
		assert.False(t, got.Fresh)
		assert.Equal(t, now.Add(-47*time.Hour), got.Updated)
		assert.Equal(t, now.Add(-47*time.Hour+5*time.Minute), got.Expires)
		require.NotNil(t, got.Device)
		assert.Equal(t, "laptop", got.Device.Category)
		assert.False(t, got.Device.Fresh)
		got.Device.Category = "phone" // Returned snapshots must not mutate retention.
	}
	old := m.mdnsNames[address]
	old.name.Updated = now.Add(-48 * time.Hour)
	m.mdnsNames[address] = old
	assert.Equal(t, "IoT device", m.Get(address).Name)
}

func TestRetainedDiscoveryExpiresAtConfirmationBoundary(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	found := Name{Name: "example-iphone.local", Source: "mdns", Updated: now, Expires: now.Add(5 * time.Minute), Fresh: true}
	unknown := Name{Source: "unknown"}
	assert.Equal(t, found.Name, mergeDiscovered(unknown, found, now.Add(48*time.Hour-time.Nanosecond)).Name)
	assert.Empty(t, mergeDiscovered(unknown, found, now.Add(48*time.Hour)).Name)
	for _, source := range []string{"override", "local", "dhcp", "hosts", "router-ptr"} {
		primary := Name{Name: "Replacement device", Source: source, Fresh: true}
		assert.Equal(t, primary.Name, mergeDiscovered(primary, found, now.Add(time.Hour)).Name)
	}
}

func TestAuthoritativeNamesKeepRetainedDeviceTypeAheadOfGuess(t *testing.T) {
	now := time.Now()
	found := Name{Name: "example-speaker.local", Source: "mdns", Updated: now.Add(-time.Hour), Expires: now.Add(-time.Minute),
		Device: &Enrichment{Hostname: "example-speaker.local", Category: "speaker", Evidence: []Evidence{}}}
	for _, source := range []string{"override", "local", "dhcp"} {
		n := mergeDiscovered(Name{Name: "porch-unit.home.arpa", Source: source, Fresh: true}, found, now)
		n = applyDNSGuess(n, netip.MustParseAddr("192.0.2.20"), []DNSActivity{{Domain: "fw-eventstream.ring.com", First: now.Add(-time.Minute), Last: now, Corroborated: now.Add(-time.Minute), Count: 2}}, now)
		assert.Equal(t, "porch-unit.home.arpa", n.Name)
		assert.True(t, n.Fresh)
		require.NotNil(t, n.Device)
		assert.Equal(t, "speaker", n.Device.Category)
		assert.False(t, n.Device.Fresh)
	}
}

func TestPartialExpiryPublicationPreservesFriendlyIdentity(t *testing.T) {
	address := netip.MustParseAddr("192.0.2.20")
	view, err := NewView(Settings{MDNS: MDNSSettings{Enabled: true}}, nil, nil)
	require.NoError(t, err)
	m := New(func() *View { return view })
	f := &fakeMDNS{packets: make(chan mdnsDatagram, 4), sent: make(chan []byte, 16)}
	m.openMDNS = func(context.Context, MDNSSettings) (mdnsTransport, []string) { return f, nil }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); m.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	m.mdnsObserve <- address
	f.packets <- mdnsDatagram{iface: 1, wire: mdnsPacket(t,
		rr(t, "20.2.0.192.in-addr.arpa. 4 IN PTR Android.local."),
		rr(t, "Android.local. 4 IN A 192.0.2.20"),
		rr(t, "_googlecast._tcp.local. 4 IN PTR 0123456789abcdef._googlecast._tcp.local."),
		rr(t, "0123456789abcdef._googlecast._tcp.local. 4 IN SRV 0 0 8009 Android.local."),
		rr(t, "0123456789abcdef._googlecast._tcp.local. 2 IN TXT \"fn=Example Display\" \"device_type=tv\""))}
	require.Eventually(t, func() bool { return m.Get(address).Name == "Example Display" }, 3*time.Second, 10*time.Millisecond)
	require.Eventually(t, func() bool { return m.Diagnostics().Records == 4 }, 4*time.Second, 10*time.Millisecond)
	partial := m.Get(address)
	assert.Equal(t, "Example Display", partial.Name)
	assert.Equal(t, "tv", partial.Device.Category)
	assert.False(t, partial.Fresh)
	require.Eventually(t, func() bool { return m.Diagnostics().Records == 0 }, 4*time.Second, 10*time.Millisecond)
	assert.Equal(t, "Example Display", m.Get(address).Name)
	assert.Equal(t, "tv", m.Get(address).Device.Category)
}

func TestRetainedIdentityAcceptsPositiveChangesWithoutRenewingOnInformationLoss(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	old := Name{Name: "Office display", Source: "dns-sd", Updated: now, Expires: now.Add(time.Minute),
		Device: &Enrichment{Hostname: "android.local", Category: "tv", Model: "Example TV"}}
	for _, tt := range []struct {
		name, label, host, category, model string
	}{
		{"renamed", "Meeting room display", "android.local", "tv", "Example TV"},
		{"new hostname", "work-macbook.local", "work-macbook.local", "laptop", ""},
		{"new model", "android.local", "android.local", "tv", "Replacement TV"},
		{"new category", "android.local", "android.local", "speaker", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			live := Name{Name: tt.label, Source: "mdns", Updated: now.Add(time.Hour), Expires: now.Add(time.Hour + time.Minute), Fresh: true,
				Device: &Enrichment{Hostname: tt.host, Category: tt.category, Model: tt.model, Fresh: true}}
			got := preferDiscovered(old, live, now.Add(time.Hour))
			assert.Equal(t, tt.label, got.Name)
			assert.Equal(t, tt.model, got.Device.Model)
			assert.Equal(t, tt.category, got.Device.Category)
			assert.True(t, got.Fresh)
		})
	}
	live := Name{Name: "android.local", Source: "mdns", Updated: now.Add(47 * time.Hour), Expires: now.Add(49 * time.Hour), Fresh: true,
		Device: &Enrichment{Hostname: "android.local", Category: "unknown", Fresh: true}}
	remembered := preferDiscovered(old, live, now.Add(47*time.Hour))
	assert.Equal(t, "Office display", remembered.Name)
	assert.Equal(t, now, remembered.Updated)
	assert.False(t, remembered.Fresh)
	assert.Equal(t, "android.local", preferDiscovered(remembered, live, now.Add(48*time.Hour)).Name)
}

func TestExpiredEvidenceDoesNotActAsAConfirmedReplacement(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tt := range []struct{ host, label, kind string }{
		{"work-macbook.local", "", "desktop"},
		{"android.local", "Preferred display", "tv"},
	} {
		t.Run(tt.host, func(t *testing.T) {
			n := Name{Name: tt.host, Source: "mdns", Updated: now, Expires: now.Add(time.Minute), Device: &Enrichment{Hostname: tt.host, Evidence: []Evidence{
				{Source: "mdns", Hostname: tt.host, Updated: now, Expires: now.Add(time.Minute)},
				{Source: "dns-sd", Hostname: tt.host, ServiceType: "_airplay._tcp", Label: tt.label, DeviceType: tt.kind, Updated: now, Expires: now.Add(time.Second)},
				{Source: "dns-sd", Hostname: tt.host, ServiceType: "_other._tcp", Label: "Secondary display", Updated: now, Expires: now.Add(time.Minute)},
			}}}
			original := enrichDiscovered(n, now)
			got := mergeDiscovered(Name{Source: "unknown"}, original, now.Add(2*time.Second))
			assert.Equal(t, original.Name, got.Name)
			assert.Equal(t, tt.kind, got.Device.Category)
			assert.False(t, got.Fresh)
		})
	}
}

func TestIdentityConfirmationIncludesNewMetadata(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := newMDNSCache()
	require.NoError(t, c.ingest(1, mdnsPacket(t,
		rr(t, "20.2.0.192.in-addr.arpa. 120 IN PTR Android.local."),
		rr(t, "Android.local. 120 IN A 192.0.2.20"),
		rr(t, "_googlecast._tcp.local. 120 IN PTR 0123456789abcdef._googlecast._tcp.local."),
		rr(t, "0123456789abcdef._googlecast._tcp.local. 120 IN SRV 0 0 8009 Android.local.")), now))
	require.NoError(t, c.ingest(1, mdnsPacket(t,
		rr(t, "0123456789abcdef._googlecast._tcp.local. 120 IN TXT \"fn=Example Display\" \"device_type=tv\"")), now.Add(time.Minute)))
	n := enrichDiscovered(c.lookup(netip.MustParseAddr("192.0.2.20"), now.Add(time.Minute)), now.Add(time.Minute))
	assert.Equal(t, now.Add(time.Minute), n.Updated)
	assert.Equal(t, "Example Display", mergeDiscovered(Name{}, n, now.Add(48*time.Hour)).Name)
	assert.Empty(t, mergeDiscovered(Name{}, n, now.Add(48*time.Hour+time.Minute)).Name)
}
