package clients

import (
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testGuessYAML = `
# Synthetic product families, independent of the bundled catalogue.
- id: washer
  name: Example washer
  category: appliance
  icon: washing-machine
  reason: Queries to washer services
  domains:
    - firmware.washer.example
    - events.washer.example
- id: other
  name: Other device
  domains:
    - firmware.other.example
`

func TestDNSGuessYAML(t *testing.T) {
	rules, err := parseDNSGuessRules(testGuessYAML)
	require.NoError(t, err)
	now := time.Now()
	got := rules.guess(netip.MustParseAddr("192.0.2.20"), []DNSActivity{
		{Domain: "firmware.washer.example", First: now, Last: now, Count: 1},
		{Domain: "events.washer.example", First: now, Last: now, Count: 1},
	}, now)
	assert.Equal(t, "Example washer", got.Name)
	assert.Equal(t, "dns-guess", got.Source)
	require.NotNil(t, got.Device)
	assert.Equal(t, "appliance", got.Device.Category)
	assert.Equal(t, "washing-machine", got.Device.Icon)
	assert.True(t, got.Device.Inferred)
	require.NotNil(t, got.Device.DNSGuess)
	assert.Equal(t, "washer", got.Device.DNSGuess.Rule)
	assert.Equal(t, "Queries to washer services", got.Device.DNSGuess.Reason)
	assert.Equal(t, now.Add(DNSGuessLifetime), got.Expires)
	require.Len(t, got.Device.DNSGuess.Domains, 2)
	assert.Equal(t, "events.washer.example", got.Device.DNSGuess.Domains[0].Domain)
	assert.Equal(t, "firmware.washer.example", got.Device.DNSGuess.Domains[1].Domain)
	exact, _ := rules.selectors()
	assert.ElementsMatch(t, []string{"firmware.washer.example", "events.washer.example", "firmware.other.example"}, exact)
	minimal := rules.guess(got.Address, []DNSActivity{{Domain: "firmware.other.example", First: now.Add(-time.Minute), Last: now, Count: 2}}, now)
	require.NotNil(t, minimal.Device)
	assert.Equal(t, "unknown", minimal.Device.Category)
	assert.Empty(t, minimal.Device.Icon)
	assert.NotEmpty(t, minimal.Device.Reason)
}

func TestDNSGuessYAMLValidation(t *testing.T) {
	for _, tc := range []struct{ name, yaml string }{
		{"missing id", "- name: Device\n  domains: [fw.example]"},
		{"reserved id", "- id: aws-iot\n  name: Device\n  domains: [fw.example]"},
		{"missing name", "- id: device\n  domains: [fw.example]"},
		{"missing domains", "- id: device\n  name: Device"},
		{"unknown field", testGuessYAML + "  typo: value\n"},
		{"invalid category", strings.Replace(testGuessYAML, "appliance", "washer", 1)},
		{"invalid icon", strings.Replace(testGuessYAML, "washing-machine", "not-a-lucide-icon", 1)},
		{"duplicate id", strings.Replace(testGuessYAML, "id: other", "id: washer", 1)},
		{"duplicate domain", strings.Replace(testGuessYAML, "firmware.other.example", "firmware.washer.example", 1)},
		{"wildcard", strings.Replace(testGuessYAML, "firmware.other.example", "*.other.example", 1)},
		{"uppercase", strings.Replace(testGuessYAML, "firmware.other.example", "FIRMWARE.other.example", 1)},
		{"extra document", testGuessYAML + "\n---\n[]\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseDNSGuessRules(tc.yaml)
			assert.Error(t, err)
		})
	}
	_, err := parseDNSGuessRules("[]")
	assert.NoError(t, err, "all exact-match families may be removed")
	_, err = parseDNSGuessRules(builtinDNSGuessYAML)
	assert.NoError(t, err, "the shipped catalogue must be valid")
}

func TestDNSGuessCorroborationAndExpiry(t *testing.T) {
	rules, err := parseDNSGuessRules(testGuessYAML)
	require.NoError(t, err)
	now := time.Now()
	firmware := DNSActivity{Domain: "firmware.washer.example", First: now.Add(-2 * time.Minute), Last: now, Count: 2}
	for _, tc := range []struct {
		name string
		rows []DNSActivity
		want string
	}{
		{"repeated firmware", []DNSActivity{firmware}, "Example washer"},
		{"one query", []DNSActivity{{Domain: firmware.Domain, First: now, Last: now, Count: 1}}, ""},
		{"A AAAA burst", []DNSActivity{{Domain: firmware.Domain, First: now, Last: now, Count: 2}}, ""},
		{"duplicate aggregate", []DNSActivity{{Domain: firmware.Domain, First: now, Last: now, Count: 1}, {Domain: firmware.Domain, First: now, Last: now, Count: 1}}, ""},
		{"website", []DNSActivity{{Domain: "washer.example", First: firmware.First, Last: now, Count: 20}}, ""},
		{"suffix spoof", []DNSActivity{{Domain: "firmware.washer.example.evil.example", First: firmware.First, Last: now, Count: 20}}, ""},
		{"IoT", []DNSActivity{{Domain: "example-ats.iot.eu-west-1.amazonaws.com", First: firmware.First, Last: now, Count: 3}}, "IoT device"},
		{"generic AWS", []DNSActivity{{Domain: "example.s3.eu-west-1.amazonaws.com", First: firmware.First, Last: now, Count: 3}}, ""},
		{"IoT lookalike", []DNSActivity{{Domain: "example-ats.iot.eu-west-1.amazonaws.com.example.org", First: firmware.First, Last: now, Count: 3}}, ""},
		{"specific beats generic", []DNSActivity{firmware, {Domain: "example-ats.iot.eu-west-1.amazonaws.com", First: firmware.First, Last: now, Count: 3}}, "Example washer"},
		{"conflicting families", []DNSActivity{firmware, {Domain: "firmware.other.example", First: now, Last: now, Count: 1}}, ""},
		{"expired", []DNSActivity{{Domain: firmware.Domain, First: now.Add(-26 * time.Hour), Last: now.Add(-24 * time.Hour), Count: 3}}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := rules.guess(netip.MustParseAddr("192.0.2.20"), tc.rows, now)
			assert.Equal(t, tc.want, got.Name)
		})
	}
	pair := now.Add(-time.Minute)
	rows := []DNSActivity{{Domain: firmware.Domain, First: now.Add(-24*time.Hour + time.Second), Last: now.Add(-time.Second), Corroborated: pair, Count: 1000}}
	got := rules.guess(netip.MustParseAddr("192.0.2.20"), rows, now.Add(2*time.Second))
	assert.Equal(t, "Example washer", got.Name)
	assert.Equal(t, pair.Add(DNSGuessLifetime), got.Expires)
	assert.Empty(t, rules.guess(got.Address, rows, pair.Add(DNSGuessLifetime)).Name)
}

func TestDNSGuessIsLowestPriorityAndSnapshotIsOwned(t *testing.T) {
	now := time.Now()
	ip := netip.MustParseAddr("192.0.2.20")
	v, err := NewView(Settings{}, nil, nil)
	require.NoError(t, err)
	m := New(func() *View { return v })
	// The structured fallback also works with an empty exact-match catalogue.
	domain := "example-ats.iot.eu-west-1.amazonaws.com"
	rows := []DNSActivity{{Address: ip, Domain: domain, First: now.Add(-2 * time.Minute), Last: now, Count: 2}}
	m.ReplaceDNSActivity(rows)
	rows[0].Domain = "changed.example"
	got := m.Get(ip)
	assert.Equal(t, "IoT device", got.Name)
	require.NotNil(t, got.Device)
	require.NotNil(t, got.Device.DNSGuess)
	got.Device.DNSGuess.Domains[0].Domain = "mutated.example"
	assert.Equal(t, domain, m.Get(ip).Device.DNSGuess.Domains[0].Domain)
	rows[0].Domain = domain
	for _, source := range []string{"override", "local", "hosts", "router-ptr", "mdns", "dns-sd", "spotify-connect"} {
		named := Name{Name: "Actual name", Source: source, Fresh: true}
		assert.Equal(t, "Actual name", applyDNSGuess(named, ip, rows, now).Name, source)
	}
	m.ReplaceDNSActivity(nil)
	assert.Empty(t, m.Get(ip).Name)
}

func TestConfiguredIconWinsAndClearingRestoresAutomatic(t *testing.T) {
	v, err := NewView(Settings{}, nil, nil)
	require.NoError(t, err)
	m := New(func() *View { return v })
	ip := netip.MustParseAddr("192.0.2.20")
	now := time.Now()
	m.mdnsNames[ip] = entry{view: v, name: Name{
		Address: ip, Name: "Example washer", Source: "mdns", Fresh: true,
		Updated: now, Expires: now.Add(time.Hour),
		Device: &Enrichment{Icon: "washing-machine", Category: "appliance", Fresh: true},
	}}
	icon := "bell"
	m.SetIcon(func(view *View, address netip.Addr) string { return icon })
	assert.Equal(t, "bell", m.Get(ip).Device.Icon)
	icon = ""
	assert.Equal(t, "washing-machine", m.Get(ip).Device.Icon)
}
