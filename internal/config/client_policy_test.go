package config

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"testing"

	"github.com/richkeenan/dimsum/internal/lists"
	"github.com/richkeenan/dimsum/internal/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

func TestLegacyClientJSONContract(t *testing.T) {
	b, err := json.Marshal(ClientOverride{Address: "192.0.2.1", Name: "Legacy"})
	require.NoError(t, err)
	var v map[string]any
	require.NoError(t, json.Unmarshal(b, &v))
	assert.Equal(t, "192.0.2.1", v["Address"])
	assert.Equal(t, "Legacy", v["Name"])
}

func TestClientRouteInheritanceAndOwnership(t *testing.T) {
	c := policyFixture()
	c.DNS.Upstreams = []string{"192.0.2.53:53"}
	c.Profiles[0].Policy.Upstream = &UpstreamRoute{Upstreams: []string{"192.0.2.54:53"}}
	c.Clients[1].Overrides.Upstream = &UpstreamRoute{Upstreams: []string{"192.0.2.55:53"}}
	v := compileClients(t, c)
	device, _ := v.Client("tablet")
	profile, _ := v.Profile("kids")
	assert.NotEqual(t, device.RouteID(), profile.RouteID())
	assert.NotEqual(t, profile.RouteID(), v.Network().RouteID())
	assert.Equal(t, policy.ClientScope, device.UpstreamSource().Kind)
	c.Clients[1].Overrides.Upstream = nil
	next := compileClients(t, c)
	reset, _ := next.Client("tablet")
	assert.Equal(t, profile.RouteID(), reset.RouteID())
	assert.Equal(t, policy.ProfileScope, reset.UpstreamSource().Kind)
	options := reset.UpstreamOptions()
	options.Endpoints[0] = v.Network().UpstreamOptions().Endpoints[0]
	assert.NotEqual(t, options, reset.UpstreamOptions())
	c.Profiles[0].Policy.Upstream.Upstreams[0] = "192.0.2.56:53"
	assert.Equal(t, profile.RouteID(), reset.RouteID(), "configuration mutation cannot change published routes")
}

func TestPolicyCollectionBudgets(t *testing.T) {
	for _, tt := range []struct {
		name   string
		change func(*Config)
	}{
		{"profiles", func(c *Config) { c.Profiles = make([]Profile, 257) }},
		{"clients", func(c *Config) { c.Clients = make([]ClientOverride, 4097) }},
		{"routes", func(c *Config) {
			for i := range 64 {
				c.Profiles = append(c.Profiles, Profile{ID: fmt.Sprintf("p%d", i), Policy: PolicyOverrides{Upstream: &UpstreamRoute{Upstreams: []string{fmt.Sprintf("192.0.2.%d:53", i+1)}}}})
			}
		}},
		{"selectors", func(c *Config) {
			p := ClientOverride{ID: "many"}
			for i := range 16385 {
				p.Selectors.Addresses = append(p.Selectors.Addresses, fmt.Sprintf("2001:db8::%x", i+1))
			}
			c.Clients = []ClientOverride{p}
		}},
		{"aggregate rules", func(c *Config) {
			c.Rules = make([]CustomRule, 100000)
			c.Profiles = []Profile{{ID: "p", Policy: PolicyOverrides{Rules: []CustomRule{{ID: "extra"}}}}}
		}},
	} {
		t.Run(tt.name, func(t *testing.T) { c := Default(); tt.change(&c); assert.Error(t, validateClientPolicy(c)) })
	}
}

func boolPtr(v bool) *bool { return &v }
func policyFixture() Config {
	c := Default()
	c.Lists = []lists.Subscription{{ID: "ads", URL: "https://example.test/list", Dialect: lists.Domains, DomainKind: policy.Exact, Enabled: true}}
	c.Clients = []ClientOverride{{Address: "192.0.2.1", Name: "Legacy"}, {ID: "tablet", Name: "Tablet", Selectors: ClientSelectors{Addresses: []string{"192.0.2.2", "2001:db8::2"}}, Profile: "kids"}}
	c.Profiles = []Profile{{ID: "kids", Policy: PolicyOverrides{Blocking: boolPtr(false), Lists: map[string]bool{"ads": false}}}}
	return c
}
func compileClients(t testing.TB, c Config) *ClientPolicies {
	t.Helper()
	require.NoError(t, Validate(c))
	base, err := policy.CompileSnapshot(1, []policy.Rule{{ID: "3:ads:1", SourceID: "ads", Kind: policy.Exact, Class: policy.SubscriptionDeny, Pattern: "ads.example"}}, policy.DefaultLimits())
	require.NoError(t, err)
	v, err := c.CompileClientPolicies(7, base)
	require.NoError(t, err)
	return v
}
func TestClientInheritanceResetAndClone(t *testing.T) {
	c := policyFixture()
	v := compileClients(t, c)
	legacy := v.Select(netip.MustParseAddr("::ffff:192.0.2.1"), "")
	value, source := legacy.Blocking()
	assert.True(t, value)
	assert.Equal(t, policy.Scope{}, source)
	tablet, ok := v.Client("tablet")
	require.True(t, ok)
	value, source = tablet.Blocking()
	assert.False(t, value)
	assert.Equal(t, policy.Scope{Kind: policy.ProfileScope, ID: "kids"}, source)
	c.Clients[1].Overrides.Blocking = boolPtr(false)
	c.Profiles[0].Policy.Blocking = boolPtr(true)
	v = compileClients(t, c)
	tablet, _ = v.Client("tablet")
	value, source = tablet.Blocking()
	assert.False(t, value)
	assert.Equal(t, policy.ClientScope, source.Kind)
	c.Clients[1].Overrides.Blocking = nil
	v = compileClients(t, c)
	tablet, _ = v.Client("tablet")
	value, source = tablet.Blocking()
	assert.True(t, value)
	assert.Equal(t, policy.ProfileScope, source.Kind)
	b, err := yaml.Marshal(c)
	require.NoError(t, err)
	d, err := Parse(b)
	require.NoError(t, err)
	clone := d.Config()
	*clone.Profiles[0].Policy.Blocking = false
	clone.Profiles[0].Policy.Lists["ads"] = true
	clone.Clients[1].Selectors.Addresses[0] = "192.0.2.99"
	assert.True(t, *d.Config().Profiles[0].Policy.Blocking)
	assert.False(t, d.Config().Profiles[0].Policy.Lists["ads"])
	assert.Equal(t, "192.0.2.2", d.Config().Clients[1].Selectors.Addresses[0])
	assert.Equal(t, b, d.Bytes())
}
func TestClientSelectorsPrecedence(t *testing.T) {
	c := Default()
	c.Clients = []ClientOverride{
		{ID: "wide", Selectors: ClientSelectors{CIDRs: []string{"192.0.2.0/24"}}},
		{ID: "narrow", Selectors: ClientSelectors{CIDRs: []string{"192.0.2.128/25"}}},
		{ID: "mac", Selectors: ClientSelectors{MACs: []string{"02:00:00:00:00:01"}}},
		{ID: "exact", Selectors: ClientSelectors{Addresses: []string{"192.0.2.140"}}},
	}
	v := compileClients(t, c)
	for _, tt := range []struct{ ip, mac, want string }{{"192.0.2.1", "", "wide"}, {"192.0.2.130", "", "narrow"}, {"192.0.2.130", "02:00:00:00:00:01", "mac"}, {"::ffff:192.0.2.140", "02:00:00:00:00:01", "exact"}, {"2001:db8::1", "", ""}} {
		assert.Equal(t, tt.want, v.Select(netip.MustParseAddr(tt.ip), tt.mac).ClientID())
	}
}
func TestClientValidation(t *testing.T) {
	for _, tt := range []struct {
		name   string
		change func(*Config)
	}{
		{"unknown profile", func(c *Config) { c.Clients[1].Profile = "missing" }},
		{"unknown list", func(c *Config) { c.Clients[1].Overrides.Lists = map[string]bool{"missing": true} }},
		{"duplicate mapped IP", func(c *Config) { c.Clients[1].Selectors.Addresses = []string{"::ffff:192.0.2.1"} }},
		{"duplicate ID", func(c *Config) { c.Clients[0].ID = "tablet" }},
		{"duplicate CIDR", func(c *Config) {
			c.Clients[0].Selectors.CIDRs = []string{"192.0.2.1/24"}
			c.Clients[1].Selectors.CIDRs = []string{"192.0.2.0/24"}
		}},
		{"duplicate MAC", func(c *Config) {
			c.Clients[0].Selectors.MACs = []string{"02:00:00:00:00:01"}
			c.Clients[1].Selectors.MACs = []string{"02-00-00-00-00-01"}
		}},
		{"route loop", func(c *Config) {
			c.Clients[1].Overrides.Upstream = &UpstreamRoute{Upstreams: []string{"127.0.0.1:5353"}}
		}},
	} {
		t.Run(tt.name, func(t *testing.T) { c := policyFixture(); tt.change(&c); assert.Error(t, Validate(c)) })
	}
}
func TestListApplicationAndScopedConfigRules(t *testing.T) {
	c := policyFixture()
	c.Lists[0].DefaultApply = boolPtr(false)
	c.Clients[1].Overrides.Lists = map[string]bool{"ads": true}
	v := compileClients(t, c)
	n, _ := policy.NormalizeName("ads.example")
	assert.Equal(t, policy.Forward, v.Network().Policy().Match(n).Result)
	tablet, _ := v.Client("tablet")
	assert.Equal(t, policy.Block, tablet.Policy().Match(n).Result)
	c.Rules = []CustomRule{{ID: "network", Enabled: true, Kind: policy.Exact, Action: "allow", Pattern: "ads.example"}}
	c.Profiles[0].Policy.Rules = []CustomRule{{ID: "profile", Enabled: true, Kind: policy.Exact, Action: "deny", Pattern: "ads.example"}}
	v = compileClients(t, c)
	tablet, _ = v.Client("tablet")
	assert.Equal(t, policy.ProfileScope, tablet.Policy().Match(n).Scope.Kind)
}
func BenchmarkClientSelection(b *testing.B) {
	c := policyFixture()
	c.Clients = append(c.Clients, ClientOverride{ID: "cidr", Selectors: ClientSelectors{CIDRs: []string{"2001:db8:1::/48"}}}, ClientOverride{ID: "mac", Selectors: ClientSelectors{MACs: []string{"02:00:00:00:00:01"}}})
	v := compileClients(b, c)
	for _, tt := range []struct{ name, ip, mac string }{{"exact", "192.0.2.2", ""}, {"MAC", "192.0.2.3", "02:00:00:00:00:01"}, {"CIDR", "2001:db8:1::3", ""}, {"default", "2001:db8:2::3", ""}} {
		b.Run(tt.name, func(b *testing.B) {
			addr := netip.MustParseAddr(tt.ip)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				v.Select(addr, tt.mac)
			}
		})
	}
}
