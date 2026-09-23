package clients

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"net/netip"
	"testing"
	"time"
)

func TestDNSGuessCorroborationAndExpiry(t *testing.T) {
	now := time.Now()
	ring := DNSActivity{Domain: "fw-eventstream.ring.com", First: now.Add(-2 * time.Minute), Last: now, Count: 2}
	cases := []struct {
		name string
		rows []DNSActivity
		want string
	}{
		{"repeated firmware", []DNSActivity{ring}, "Ring device"},
		{"one query", []DNSActivity{{Domain: ring.Domain, First: now, Last: now, Count: 1}}, ""},
		{"A AAAA burst", []DNSActivity{{Domain: ring.Domain, First: now, Last: now, Count: 2}}, ""},
		{"duplicate aggregate", []DNSActivity{{Domain: ring.Domain, First: now, Last: now, Count: 1}, {Domain: ring.Domain, First: now, Last: now, Count: 1}}, ""},
		{"complementary domains", []DNSActivity{{Domain: ring.Domain, First: now, Last: now, Count: 1}, {Domain: "fw-snaps.prod.gws.ring.amazon.dev", First: now, Last: now, Count: 1}}, "Ring device"},
		{"website", []DNSActivity{{Domain: "ring.com", First: ring.First, Last: now, Count: 20}}, ""},
		{"suffix spoof", []DNSActivity{{Domain: "fw-eventstream.ring.com.example.org", First: ring.First, Last: now, Count: 20}}, ""},
		{"shared time", []DNSActivity{{Domain: "time.aws.com", First: ring.First, Last: now, Count: 20}}, ""},
		{"IoT", []DNSActivity{{Domain: "example-ats.iot.eu-west-1.amazonaws.com", First: ring.First, Last: now, Count: 3}}, "IoT device"},
		{"IoT repeated reconnects", []DNSActivity{{Domain: "example-ats.iot.eu-west-1.amazonaws.com", First: now.Add(-35 * time.Second), Last: now, Count: 3}}, "IoT device"},
		{"generic AWS", []DNSActivity{{Domain: "example.s3.eu-west-1.amazonaws.com", First: ring.First, Last: now, Count: 3}}, ""},
		{"IoT lookalike", []DNSActivity{{Domain: "example-ats.iot.eu-west-1.amazonaws.com.example.org", First: ring.First, Last: now, Count: 3}}, ""},
		{"specific beats generic", []DNSActivity{ring, {Domain: "example-ats.iot.eu-west-1.amazonaws.com", First: ring.First, Last: now, Count: 3}}, "Ring device"},
		{"conflicting vendors", []DNSActivity{ring, {Domain: "sun.hac.lp1.d4c.nintendo.net", First: ring.First, Last: now, Count: 3}}, ""},
		{"Nintendo platform", []DNSActivity{{Domain: "sun.hac.lp1.d4c.nintendo.net", First: ring.First, Last: now, Count: 3}}, "Nintendo Switch"},
		{"shared Nintendo", []DNSActivity{{Domain: "ctest.cdn.nintendo.net", First: ring.First, Last: now, Count: 3}}, ""},
		{"expired", []DNSActivity{{Domain: ring.Domain, First: now.Add(-26 * time.Hour), Last: now.Add(-24 * time.Hour), Count: 3}}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := dnsGuess(netip.MustParseAddr("192.0.2.20"), tc.rows, now)
			assert.Equal(t, tc.want, got.Name)
			if tc.want != "" {
				require.NotNil(t, got.Device)
				require.NotNil(t, got.Device.DNSGuess)
				assert.True(t, got.Device.Inferred)
				assert.Equal(t, "dns-guess", got.Source)
			}
		})
	}
}

func TestDNSGuessReolink(t *testing.T) {
	now := time.Now()
	ip := netip.MustParseAddr("192.0.2.20")
	for _, tc := range []struct {
		name, domain string
		span         time.Duration
		count        uint64
		want         bool
	}{
		{"repeated push queries", "pushx.reolink.com", 83 * time.Second, 4, true},
		{"single query", "pushx.reolink.com", 0, 1, false},
		{"A AAAA burst", "pushx.reolink.com", 0, 2, false},
		{"website", "reolink.com", 83 * time.Second, 4, false},
		{"suffix spoof", "pushx.reolink.com.example.org", 83 * time.Second, 4, false},
		{"unrecognised subdomain", "www.reolink.com", 83 * time.Second, 4, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := dnsGuess(ip, []DNSActivity{{Domain: tc.domain, First: now.Add(-tc.span), Last: now, Count: tc.count}}, now)
			if !tc.want {
				assert.Empty(t, got.Name)
				return
			}
			assert.Equal(t, "Reolink device", got.Name)
			assert.Equal(t, "dns-guess", got.Source)
			require.NotNil(t, got.Device)
			assert.Equal(t, "camera", got.Device.Category)
			assert.True(t, got.Device.Inferred)
			require.NotNil(t, got.Device.DNSGuess)
			assert.Equal(t, "reolink", got.Device.DNSGuess.Rule)
			require.Len(t, got.Device.DNSGuess.Domains, 1)
			assert.Equal(t, "pushx.reolink.com", got.Device.DNSGuess.Domains[0].Domain)
		})
	}
	// Retained history must select this endpoint before it can be attributed.
	exact, _ := DNSGuessSelectors()
	assert.Contains(t, exact, "pushx.reolink.com")
}

func TestDNSGuessRecentPairSurvivesOldestQueryExpiry(t *testing.T) {
	now := time.Now()
	pair := now.Add(-time.Minute)
	rows := []DNSActivity{{Domain: "fw-eventstream.ring.com", First: now.Add(-24*time.Hour + time.Second), Last: now.Add(-time.Second), Corroborated: pair, Count: 1000}}
	got := dnsGuess(netip.MustParseAddr("192.0.2.20"), rows, now.Add(2*time.Second))
	assert.Equal(t, "Ring device", got.Name)
	assert.Equal(t, pair.Add(DNSGuessLifetime), got.Expires)
	assert.Empty(t, dnsGuess(netip.MustParseAddr("192.0.2.20"), rows, pair.Add(DNSGuessLifetime)).Name)
}

func TestDNSGuessIsLowestPriorityAndSnapshotIsOwned(t *testing.T) {
	now := time.Now()
	ip := netip.MustParseAddr("192.0.2.20")
	v, err := NewView(Settings{}, nil, nil)
	require.NoError(t, err)
	m := New(func() *View { return v })
	rows := []DNSActivity{{Address: ip, Domain: "fw-eventstream.ring.com", First: now.Add(-2 * time.Minute), Last: now, Count: 2}}
	m.ReplaceDNSActivity(rows)
	rows[0].Domain = "changed.example"
	got := m.Get(ip)
	assert.Equal(t, "Ring device", got.Name)
	require.NotNil(t, got.Device.DNSGuess)
	got.Device.DNSGuess.Domains[0].Domain = "mutated.example"
	assert.Equal(t, "fw-eventstream.ring.com", m.Get(ip).Device.DNSGuess.Domains[0].Domain)
	for _, source := range []string{"override", "local", "hosts", "router-ptr", "mdns", "dns-sd", "spotify-connect"} {
		named := Name{Name: "Actual name", Source: source, Fresh: true}
		assert.Equal(t, "Actual name", applyDNSGuess(named, ip, rows, now).Name, source)
	}
	m.ReplaceDNSActivity(nil)
	assert.Empty(t, m.Get(ip).Name)
}
