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
