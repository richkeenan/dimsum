package clients

import (
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDHCPNamesKeepDiscoveryAndMetadata(t *testing.T) {
	now := time.Now()
	a := netip.MustParseAddr("192.168.50.100")
	v, err := NewView(Settings{MDNS: MDNSSettings{Enabled: true}}, nil, nil)
	require.NoError(t, err)
	m := New(func() *View { return v })
	lease := Name{Address: a, Name: "studio.home.arpa", Source: "dhcp", Fresh: true, Expires: now.Add(time.Minute)}
	generated := false
	m.SetDHCP(func(view *View, address netip.Addr) (Name, bool) {
		if view != v || address != a {
			return Name{}, false
		}
		return lease, generated
	})
	m.mdnsNames[a] = entry{v, Name{Address: a, Name: "Studio speaker", Source: "dns-sd", Fresh: true, Expires: now.Add(time.Minute), Device: &Enrichment{Evidence: []Evidence{{Source: "dns-sd", Label: "Studio speaker", ServiceType: "_raop._tcp.local", Model: "AudioAccessory5,1", Updated: now, Expires: now.Add(time.Minute)}}}}}
	n := m.Get(a)
	assert.Equal(t, "studio.home.arpa", n.Name)
	assert.Equal(t, "dhcp", n.Source)
	require.NotNil(t, n.Device)
	assert.Equal(t, "speaker", n.Device.Category, "the UI selects the speaker icon from this category even when DHCP supplies the name")
	assert.NotEmpty(t, n.Device.Evidence)
	assert.NotEmpty(t, m.mdnsObserve, "DHCP must keep multicast discovery interested")
	assert.NotEmpty(t, m.queue, "DHCP must not stop fallback discovery")
	generated = true
	lease.Name = "host-192-168-50-100.home.arpa"
	n = m.Get(a)
	assert.Equal(t, "Studio speaker", n.Name)
	assert.Equal(t, "dns-sd", n.Source)
	generated = false
	lease.Name = "android.home.arpa"
	n = m.Get(a)
	assert.Equal(t, "Studio speaker", n.Name, "a generic DHCP label is not a useful owner-facing name")
	lease.Expires = now.Add(-time.Second)
	n = m.Get(a)
	assert.Equal(t, "Studio speaker", n.Name)
	lease = Name{} // disabled publication
	n = m.Get(a)
	assert.Equal(t, "Studio speaker", n.Name)
	lease = Name{Address: a, Name: "studio.home.arpa", Source: "dhcp", Fresh: true, Expires: now.Add(time.Minute)}
	v.overrides[a] = "Owner name"
	n = m.Get(a)
	assert.Equal(t, "Owner name", n.Name)
	require.NotNil(t, n.Device)
	assert.NotEmpty(t, n.Device.Evidence)
	delete(v.overrides, a)
	v.local[a] = "configured.home.arpa"
	n = m.Get(a)
	assert.Equal(t, "configured.home.arpa", n.Name)
	delete(v.local, a)
	lease = Name{Address: a, Name: "reserved.home.arpa", Source: "local", Fresh: true}
	n = m.Get(a)
	assert.Equal(t, "reserved.home.arpa", n.Name)
	assert.Equal(t, "local", n.Source)
	require.NotNil(t, n.Device)
	assert.Equal(t, "speaker", n.Device.Category)
}

func TestDHCPNamesRetainIndependentDNSHistoryEnrichment(t *testing.T) {
	now := time.Now()
	a := netip.MustParseAddr("192.0.2.100")
	for _, tc := range []struct {
		kind, hostname, source, wantName, wantSource string
		generated, expired                           bool
	}{
		{kind: "useful", hostname: "porch-unit.home.arpa", source: "dhcp", wantName: "porch-unit.home.arpa", wantSource: "dhcp"},
		{kind: "generated", hostname: "host-192-0-2-100.home.arpa", source: "dhcp", generated: true, wantName: "Ring device", wantSource: "dns-guess"},
		{kind: "reservation", hostname: "reserved.home.arpa", source: "local", wantName: "reserved.home.arpa", wantSource: "local"},
		{kind: "expired", hostname: "porch-unit.home.arpa", source: "dhcp", expired: true, wantName: "Ring device", wantSource: "dns-guess"},
		{kind: "disabled", wantName: "Ring device", wantSource: "dns-guess"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			v, err := NewView(Settings{}, nil, nil)
			require.NoError(t, err)
			m := New(func() *View { return v })
			expires := now.Add(time.Hour)
			if tc.expired {
				expires = now.Add(-time.Second)
			}
			m.SetDHCP(func(*View, netip.Addr) (Name, bool) {
				return Name{Address: a, Name: tc.hostname, Source: tc.source, Expires: expires, Fresh: true}, tc.generated
			})
			m.ReplaceDNSActivity([]DNSActivity{{Address: a, Domain: "fw-eventstream.ring.com", First: now.Add(-2 * time.Minute), Last: now, Count: 2}})
			n := m.Get(a)
			assert.Equal(t, tc.wantName, n.Name)
			assert.Equal(t, tc.wantSource, n.Source)
			if tc.wantSource != "dns-guess" {
				assert.Equal(t, expires, n.Expires)
			}
			require.NotNil(t, n.Device)
			assert.Equal(t, "camera", n.Device.Category)
			assert.True(t, n.Device.Inferred)
			require.NotNil(t, n.Device.DNSGuess)
			assert.Equal(t, "ring", n.Device.DNSGuess.Rule)
			require.Len(t, n.Device.DNSGuess.Domains, 1)
			assert.Equal(t, "fw-eventstream.ring.com", n.Device.DNSGuess.Domains[0].Domain)
			n.Device.DNSGuess.Domains[0].Domain = "mutated.test"
			assert.Equal(t, "fw-eventstream.ring.com", m.Get(a).Device.DNSGuess.Domains[0].Domain)
			// Independent evidence expiry must not expire a live selected DHCP name.
			m.ReplaceDNSActivity([]DNSActivity{{Address: a, Domain: "fw-eventstream.ring.com", First: now.Add(-26 * time.Hour), Last: now.Add(-25 * time.Hour), Count: 2}})
			n = m.Get(a)
			require.NotNil(t, n.Device)
			assert.Nil(t, n.Device.DNSGuess)
			if tc.wantSource != "dns-guess" {
				assert.Equal(t, tc.wantName, n.Name)
				assert.Equal(t, expires, n.Expires)
			}
		})
	}
}

func TestDHCPDNSHistoryEnrichmentDoesNotReplaceAdvertisedDevice(t *testing.T) {
	now := time.Now()
	a := netip.MustParseAddr("192.0.2.100")
	for _, source := range []string{"dns-sd", "spotify-connect"} {
		t.Run(source, func(t *testing.T) {
			v, err := NewView(Settings{}, nil, nil)
			require.NoError(t, err)
			m := New(func() *View { return v })
			m.SetDHCP(func(*View, netip.Addr) (Name, bool) {
				return Name{Address: a, Name: "porch-unit.home.arpa", Source: "dhcp", Expires: now.Add(time.Hour), Fresh: true}, false
			})
			m.ReplaceDNSActivity([]DNSActivity{{Address: a, Domain: "fw-eventstream.ring.com", First: now.Add(-2 * time.Minute), Last: now, Count: 2}})
			m.mdnsNames[a] = entry{v, Name{Address: a, Name: "Studio speaker", Source: source, Expires: now.Add(time.Minute), Fresh: true, Device: &Enrichment{Evidence: []Evidence{{Source: source, Label: "Studio speaker", DeviceType: "speaker", Model: "Fixture model", Updated: now, Expires: now.Add(time.Minute)}}}}}
			n := m.Get(a)
			assert.Equal(t, "porch-unit.home.arpa", n.Name)
			assert.Equal(t, "dhcp", n.Source)
			require.NotNil(t, n.Device)
			assert.Equal(t, "speaker", n.Device.Category)
			assert.False(t, n.Device.Inferred)
			assert.Equal(t, "Fixture model", n.Device.Model)
			require.Len(t, n.Device.Evidence, 1)
			assert.Equal(t, source, n.Device.Evidence[0].Source)
			require.NotNil(t, n.Device.DNSGuess)
			assert.Equal(t, "ring", n.Device.DNSGuess.Rule, "independent inferred evidence remains available without replacing the advertised category")
			expired := m.mdnsNames[a]
			expired.name.Expires = now.Add(-time.Second)
			expired.name.Device.Evidence[0].Expires = now.Add(-time.Second)
			m.mdnsNames[a] = expired
			n = m.Get(a)
			assert.Equal(t, "porch-unit.home.arpa", n.Name)
			require.NotNil(t, n.Device)
			assert.Equal(t, "camera", n.Device.Category)
			assert.True(t, n.Device.Inferred)
			assert.Empty(t, n.Device.Evidence)
			require.NotNil(t, n.Device.DNSGuess)
		})
	}
}

func TestDNSHistoryEnrichesConfiguredNamesWithoutReplacingThem(t *testing.T) {
	now := time.Now()
	a := netip.MustParseAddr("192.0.2.100")
	for _, source := range []string{"override", "local"} {
		t.Run(source, func(t *testing.T) {
			var overrides []Override
			var local map[netip.Addr]string
			if source == "override" {
				overrides = []Override{{Address: a.String(), Name: "Owner label"}}
			} else {
				local = map[netip.Addr]string{a: "Owner label"}
			}
			v, err := NewView(Settings{}, overrides, local)
			require.NoError(t, err)
			m := New(func() *View { return v })
			m.ReplaceDNSActivity([]DNSActivity{{Address: a, Domain: "fw-eventstream.ring.com", First: now.Add(-2 * time.Minute), Last: now, Count: 2}})
			n := m.Get(a)
			assert.Equal(t, "Owner label", n.Name)
			assert.Equal(t, source, n.Source)
			assert.True(t, n.Expires.IsZero())
			require.NotNil(t, n.Device)
			assert.Equal(t, "camera", n.Device.Category)
			assert.True(t, n.Device.Inferred)
			require.NotNil(t, n.Device.DNSGuess)
		})
	}
}
