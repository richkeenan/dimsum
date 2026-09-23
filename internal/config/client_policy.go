package config

import (
	"fmt"
	"maps"
	"net"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/richkeenan/dimsum/internal/clients"
	"github.com/richkeenan/dimsum/internal/policy"
)

// PolicyOverrides is sparse: nil pointers and absent map keys mean inherit.
// Rules are additive scoped owners; reset removes the corresponding override.
type PolicyOverrides struct {
	Blocking *bool           `yaml:"blocking,omitempty" json:"blocking,omitempty"`
	Lists    map[string]bool `yaml:"lists,omitempty" json:"lists,omitempty"`
	Upstream *UpstreamRoute  `yaml:"upstream,omitempty" json:"upstream,omitempty"`
	Rules    []CustomRule    `yaml:"rules,omitempty" json:"rules,omitempty"`
}
type UpstreamRoute struct {
	Upstreams []string `yaml:"upstreams" json:"upstreams"`
	Fallback  []string `yaml:"fallback_upstreams,omitempty" json:"fallback_upstreams,omitempty"`
}
type Profile struct {
	ID     string          `yaml:"id" json:"id"`
	Name   string          `yaml:"name,omitempty" json:"name,omitempty"`
	Policy PolicyOverrides `yaml:"policy,omitempty" json:"policy"`
}
type ClientSelectors struct {
	Addresses []string `yaml:"addresses,omitempty" json:"addresses,omitempty"`
	CIDRs     []string `yaml:"cidrs,omitempty" json:"cidrs,omitempty"`
	MACs      []string `yaml:"macs,omitempty" json:"macs,omitempty"`
}

// ClientOverride retains legacy address/name entries. Rich entries require a
// stable ID; names remain labels and never participate in identity selection.
type ClientOverride struct {
	Address     string          `yaml:"address,omitempty"`
	Name        string          `yaml:"name,omitempty"`
	ID          string          `yaml:"id,omitempty" json:"id,omitempty"`
	Selectors   ClientSelectors `yaml:"selectors,omitempty" json:"selectors,omitempty"`
	Profile     string          `yaml:"profile,omitempty" json:"profile,omitempty"`
	Overrides   PolicyOverrides `yaml:"overrides,omitempty" json:"overrides"`
	PausedUntil *time.Time      `yaml:"paused_until,omitempty" json:"paused_until,omitempty"`
}

func cloneBool(v *bool) *bool {
	if v == nil {
		return nil
	}
	b := *v
	return &b
}
func (p PolicyOverrides) clone() PolicyOverrides {
	p.Blocking = cloneBool(p.Blocking)
	p.Lists = maps.Clone(p.Lists)
	p.Rules = slices.Clone(p.Rules)
	if p.Upstream != nil {
		r := *p.Upstream
		r.Upstreams = slices.Clone(r.Upstreams)
		r.Fallback = slices.Clone(r.Fallback)
		p.Upstream = &r
	}
	return p
}
func (c *Config) cloneClientPolicy() {
	c.Blocking = cloneBool(c.Blocking)
	for i := range c.Lists {
		c.Lists[i].DefaultApply = cloneBool(c.Lists[i].DefaultApply)
	}
	c.Profiles = slices.Clone(c.Profiles)
	for i := range c.Profiles {
		c.Profiles[i].Policy = c.Profiles[i].Policy.clone()
	}
	for i := range c.Clients {
		p := &c.Clients[i]
		p.Selectors.Addresses = slices.Clone(p.Selectors.Addresses)
		p.Selectors.CIDRs = slices.Clone(p.Selectors.CIDRs)
		p.Selectors.MACs = slices.Clone(p.Selectors.MACs)
		p.Overrides = p.Overrides.clone()
		if p.PausedUntil != nil {
			v := *p.PausedUntil
			p.PausedUntil = &v
		}
	}
}

// NamingOverrides converts only explicit addresses to naming producer inputs.
func (c Config) NamingOverrides() []clients.Override {
	var out []clients.Override
	for _, p := range c.Clients {
		if p.Name == "" {
			continue
		}
		if p.Address != "" {
			out = append(out, clients.Override{Address: p.Address, Name: p.Name})
		}
		for _, a := range p.Selectors.Addresses {
			out = append(out, clients.Override{Address: a, Name: p.Name})
		}
	}
	return out
}

func clientID(c ClientOverride) string {
	if c.ID != "" {
		return c.ID
	}
	a, _ := netip.ParseAddr(c.Address)
	return "address:" + a.Unmap().String()
}
func validPolicyID(id string) bool {
	if len(id) == 0 || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}
func selectorAddress(s string) (netip.Addr, error) {
	a, e := netip.ParseAddr(s)
	if e != nil || a.Zone() != "" || a.IsUnspecified() || a.IsMulticast() {
		return netip.Addr{}, fmt.Errorf("expected unicast address")
	}
	return a.Unmap(), nil
}
func selectorPrefix(s string) (netip.Prefix, error) {
	p, e := netip.ParsePrefix(s)
	if e != nil {
		return p, e
	}
	if p.Addr().Is4In6() {
		if p.Bits() < 96 {
			return netip.Prefix{}, fmt.Errorf("mapped IPv4 prefix requires at least 96 bits")
		}
		p = netip.PrefixFrom(p.Addr().Unmap(), p.Bits()-96)
	}
	return p.Masked(), nil
}
func selectorMAC(s string) (string, error) {
	a, e := net.ParseMAC(s)
	if e != nil || len(a) != 6 || a[0]&1 != 0 {
		return "", fmt.Errorf("expected unicast 6-byte MAC")
	}
	if slices.Equal(a, net.HardwareAddr{0, 0, 0, 0, 0, 0}) {
		return "", fmt.Errorf("zero MAC")
	}
	return a.String(), nil
}

func validateClientPolicy(c Config) error {
	if len(c.Profiles) > 256 || len(c.Clients) > 4096 {
		return fmt.Errorf("clients: at most 256 profiles and 4096 clients")
	}
	profiles := map[string]bool{}
	ids := map[string]bool{}
	selectors := map[string]bool{}
	lists := map[string]bool{}
	routes := map[string]bool{}
	for _, s := range c.Lists {
		lists[s.ID] = true
	}
	totalRules := len(c.Rules)
	totalSelectors := 0
	checkPolicy := func(p PolicyOverrides) error {
		for id := range p.Lists {
			if !lists[id] {
				return fmt.Errorf("unknown list %q", id)
			}
		}
		totalRules += len(p.Rules)
		if totalRules > 100000 {
			return fmt.Errorf("at most 100000 custom rules across all scopes")
		}
		seen := map[string]bool{}
		for _, r := range p.Rules {
			if r.ID == "" || seen[r.ID] || r.Action != "allow" && r.Action != "deny" {
				return fmt.Errorf("invalid or duplicate scoped rule %q", r.ID)
			}
			seen[r.ID] = true
			if _, err := policy.Compile(0, []policy.Rule{{ID: r.ID, Kind: r.Kind, Class: policy.CustomDeny, Pattern: r.Pattern}}, policy.DefaultLimits()); err != nil {
				return err
			}
		}
		if p.Upstream != nil {
			if len(p.Upstream.Upstreams) == 0 {
				return fmt.Errorf("upstream: primary required")
			}
			key := strings.Join(p.Upstream.Upstreams, "\x00") + "\x01" + strings.Join(p.Upstream.Fallback, "\x00")
			if !routes[key] {
				routes[key] = true
				if len(routes) > 63 {
					return fmt.Errorf("upstream: at most 64 routes including network")
				}
				base := c
				base.Clients = nil
				base.Profiles = nil
				base.Rules = nil
				base.DNS.Upstreams = p.Upstream.Upstreams
				base.DNS.Fallback = p.Upstream.Fallback
				if err := Validate(base); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for i, p := range c.Profiles {
		if !validPolicyID(p.ID) || profiles[p.ID] {
			return fmt.Errorf("profiles[%d].id: invalid or duplicate", i)
		}
		profiles[p.ID] = true
		if err := checkPolicy(p.Policy); err != nil {
			return fmt.Errorf("profiles[%d]: %w", i, err)
		}
	}
	add := func(key string) error {
		if selectors[key] {
			return fmt.Errorf("duplicate selector %s", key)
		}
		selectors[key] = true
		totalSelectors++
		if totalSelectors > 16384 {
			return fmt.Errorf("at most 16384 selectors")
		}
		return nil
	}
	for i, p := range c.Clients {
		if p.ID == "" && (p.Address == "" || strings.TrimSpace(p.Name) == "" || p.Profile != "" || len(p.Selectors.Addresses)+len(p.Selectors.CIDRs)+len(p.Selectors.MACs) > 0 || p.Overrides.Blocking != nil || p.Overrides.Upstream != nil || len(p.Overrides.Lists)+len(p.Overrides.Rules) > 0 || p.PausedUntil != nil) {
			return fmt.Errorf("clients[%d]: stable ID required for policy/selectors; legacy entries require address/name", i)
		}
		id := clientID(p)
		if p.ID != "" && !validPolicyID(p.ID) || ids[id] {
			return fmt.Errorf("clients[%d].id: invalid or duplicate", i)
		}
		ids[id] = true
		if p.Profile != "" && !profiles[p.Profile] {
			return fmt.Errorf("clients[%d].profile: unknown profile", i)
		}
		if err := checkPolicy(p.Overrides); err != nil {
			return fmt.Errorf("clients[%d]: %w", i, err)
		}
		addresses := slices.Clone(p.Selectors.Addresses)
		if p.Address != "" {
			addresses = append(addresses, p.Address)
		}
		if len(addresses)+len(p.Selectors.CIDRs)+len(p.Selectors.MACs) == 0 {
			return fmt.Errorf("clients[%d]: at least one selector required", i)
		}
		for _, s := range addresses {
			a, e := selectorAddress(s)
			// Naming-only legacy entries historically accepted any parsed address.
			// Keep their canonical identity (including zones) for uniqueness; only
			// policy-suitable addresses enter the selector index at compilation.
			if p.ID == "" {
				a, e = netip.ParseAddr(s)
				a = a.Unmap()
			}
			if e != nil {
				return fmt.Errorf("clients[%d]: %w", i, e)
			}
			if e = add("ip:" + a.String()); e != nil {
				return fmt.Errorf("clients[%d]: %w", i, e)
			}
		}
		for _, s := range p.Selectors.CIDRs {
			a, e := selectorPrefix(s)
			if e != nil {
				return fmt.Errorf("clients[%d]: %w", i, e)
			}
			if e = add("cidr:" + a.String()); e != nil {
				return fmt.Errorf("clients[%d]: %w", i, e)
			}
		}
		for _, s := range p.Selectors.MACs {
			a, e := selectorMAC(s)
			if e != nil {
				return fmt.Errorf("clients[%d]: %w", i, e)
			}
			if e = add("mac:" + a); e != nil {
				return fmt.Errorf("clients[%d]: %w", i, e)
			}
		}
	}
	// One compilation enforces aggregate regex budgets across all owner scopes.
	if _, err := policy.Compile(0, c.scopedPolicyRules(), policy.DefaultLimits()); err != nil {
		return fmt.Errorf("rules: %w", err)
	}
	return nil
}

func (c Config) scopedPolicyRules() []policy.Rule {
	out := c.PolicyRules()
	add := func(p PolicyOverrides, scope policy.Scope) {
		for _, r := range p.Rules {
			if r.Enabled {
				class := policy.CustomDeny
				if r.Action == "allow" {
					class = policy.CustomAllow
				}
				out = append(out, policy.Rule{ID: fmt.Sprintf("scoped-custom:%s:%d:%s:%s", scope.Kind, len(scope.ID), scope.ID, r.ID), SourceID: "custom", SourceText: r.Pattern, Kind: r.Kind, Class: class, Pattern: r.Pattern, Scope: scope})
			}
		}
	}
	for _, p := range c.Profiles {
		add(p.Policy, policy.Scope{Kind: policy.ProfileScope, ID: p.ID})
	}
	for _, p := range c.Clients {
		add(p.Overrides, policy.Scope{Kind: policy.ClientScope, ID: clientID(p)})
	}
	return out
}
