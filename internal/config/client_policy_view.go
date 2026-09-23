package config

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"maps"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/richkeenan/dimsum/internal/policy"
	"github.com/richkeenan/dimsum/internal/upstream"
)

type effectiveBool struct {
	value  bool
	source policy.Scope
}

// EffectivePolicy is immutable. Accessors returning slices/options give owned
// copies; Policy shares the generation's compiled indexes with every device.
type EffectivePolicy struct {
	clientID, profileID string
	blocking            effectiveBool
	lists               map[string]effectiveBool
	route               *effectiveRoute
	routeSource         policy.Scope
	pausedUntil         time.Time
	matcher             *policy.PolicySnapshot
}
type effectiveRoute struct {
	id  string
	dns DNS
	key upstream.RouteKey
}

func (p *EffectivePolicy) ClientID() string               { return p.clientID }
func (p *EffectivePolicy) ProfileID() string              { return p.profileID }
func (p *EffectivePolicy) Blocking() (bool, policy.Scope) { return p.blocking.value, p.blocking.source }
func (p *EffectivePolicy) List(id string) (bool, policy.Scope) {
	v := p.lists[id]
	return v.value, v.source
}
func (p *EffectivePolicy) PausedUntil() time.Time         { return p.pausedUntil }
func (p *EffectivePolicy) Policy() *policy.PolicySnapshot { return p.matcher }
func (p *EffectivePolicy) RouteID() string                { return p.route.id }

// RouteKey is generation-local; pair it with Snapshot.Generation in caches.
func (p *EffectivePolicy) RouteKey() upstream.RouteKey       { return p.route.key }
func (p *EffectivePolicy) UpstreamSource() policy.Scope      { return p.routeSource }
func (p *EffectivePolicy) UpstreamOptions() upstream.Options { return p.route.dns.UpstreamOptions() }

// ClientPolicies owns precomputed descriptors and selector indexes. Exact IP
// beats supplied authoritative MAC, which beats longest CIDR, then network.
type ClientPolicies struct {
	network           *EffectivePolicy
	clients, profiles map[string]*EffectivePolicy
	addresses         map[netip.Addr]*EffectivePolicy
	macs              map[[6]byte]*EffectivePolicy
	prefixes          map[netip.Prefix]*EffectivePolicy
	bits4, bits6      []int
	routes            map[upstream.RouteKey]*effectiveRoute
}

// RouteOptions is a cold-path accessor returning owned transport options.
func (v *ClientPolicies) RouteOptions(key upstream.RouteKey) (upstream.Options, bool) {
	r := v.routes[key]
	if r == nil {
		return upstream.Options{}, false
	}
	return r.dns.UpstreamOptions(), true
}

func (v *ClientPolicies) Network() *EffectivePolicy { return v.network }
func (v *ClientPolicies) Client(id string) (*EffectivePolicy, bool) {
	p, ok := v.clients[id]
	return p, ok
}
func (v *ClientPolicies) Profile(id string) (*EffectivePolicy, bool) {
	p, ok := v.profiles[id]
	return p, ok
}
func (v *ClientPolicies) Select(address netip.Addr, authoritativeMAC string) *EffectivePolicy {
	address = address.Unmap().WithZone("")
	if p := v.addresses[address]; p != nil {
		return p
	}
	if key, ok := macBytes(authoritativeMAC); ok {
		if p := v.macs[key]; p != nil {
			return p
		}
	}
	bits := v.bits6
	if address.Is4() {
		bits = v.bits4
	}
	if address.IsValid() {
		for _, b := range bits {
			if p := v.prefixes[netip.PrefixFrom(address, b).Masked()]; p != nil {
				return p
			}
		}
	}
	return v.network
}

// MAC parsing avoids heap work on the selection path. Config accepts net.ParseMAC
// spellings; request identity accepts six colon- or hyphen-separated octets.
func macBytes(s string) (key [6]byte, ok bool) {
	if len(s) != 17 {
		return key, false
	}
	hex := func(b byte) (byte, bool) {
		switch {
		case b >= '0' && b <= '9':
			return b - '0', true
		case b >= 'a' && b <= 'f':
			return b - 'a' + 10, true
		case b >= 'A' && b <= 'F':
			return b - 'A' + 10, true
		}
		return 0, false
	}
	for i := range key {
		j := i * 3
		a, ok := hex(s[j])
		if !ok {
			return key, false
		}
		b, ok := hex(s[j+1])
		if !ok {
			return key, false
		}
		key[i] = a<<4 | b
		if i < 5 && s[j+2] != ':' && s[j+2] != '-' {
			return key, false
		}
	}
	return key, true
}

// CompileClientPolicies builds one shared owner overlay and interned matching
// views. subscriptions must be the flat enabled-source snapshot, not an overlay.
func (c Config) CompileClientPolicies(generation uint64, subscriptions *policy.PolicySnapshot) (*ClientPolicies, error) {
	if err := Validate(c); err != nil {
		return nil, err
	}
	return c.compileClientPolicies(generation, subscriptions)
}
func (c Config) compileClientPolicies(generation uint64, subscriptions *policy.PolicySnapshot) (*ClientPolicies, error) {
	root, err := policy.CompileOverlay(generation, c.scopedPolicyRules(), subscriptions, policy.DefaultLimits())
	if err != nil {
		return nil, err
	}
	v := &ClientPolicies{clients: map[string]*EffectivePolicy{}, profiles: map[string]*EffectivePolicy{}, addresses: map[netip.Addr]*EffectivePolicy{}, macs: map[[6]byte]*EffectivePolicy{}, prefixes: map[netip.Prefix]*EffectivePolicy{}}
	routes := map[string]*effectiveRoute{}
	v.routes = map[upstream.RouteKey]*effectiveRoute{}
	internRoute := func(d DNS) *effectiveRoute {
		b, _ := json.Marshal(d.UpstreamOptions())
		id := fmt.Sprintf("%x", sha256.Sum256(b))
		if r := routes[id]; r != nil {
			return r
		}
		d.Listen = nil
		d.Upstreams = slices.Clone(d.Upstreams)
		d.Fallback = slices.Clone(d.Fallback)
		d.BootstrapDNS = slices.Clone(d.BootstrapDNS)
		r := &effectiveRoute{id: id, dns: d, key: upstream.RouteKey(len(routes) + 1)}
		routes[id] = r
		v.routes[r.key] = r
		return r
	}
	network := &EffectivePolicy{blocking: effectiveBool{value: true}, lists: map[string]effectiveBool{}, route: internRoute(c.DNS)}
	if c.Blocking != nil {
		network.blocking.value = *c.Blocking
	}
	available := map[string]bool{}
	for _, s := range c.Lists {
		applies := s.Enabled
		if s.DefaultApply != nil {
			applies = s.Enabled && *s.DefaultApply
		}
		network.lists[s.ID] = effectiveBool{value: applies}
		available[s.ID] = s.Enabled
	}
	matchers := map[string]*policy.PolicySnapshot{}
	finish := func(p *EffectivePolicy, profile, client string) {
		var sources []string
		for id, val := range p.lists {
			if val.value {
				sources = append(sources, id)
			}
		}
		slices.Sort(sources)
		// Length framing prevents arbitrary list IDs from colliding in intern keys.
		var key strings.Builder
		fmt.Fprintf(&key, "%d:%s%d:%s", len(profile), profile, len(client), client)
		for _, s := range sources {
			fmt.Fprintf(&key, "%d:%s", len(s), s)
		}
		if m := matchers[key.String()]; m != nil {
			p.matcher = m
			return
		}
		p.matcher = root.WithSelection(policy.NewSelection(profile, client, sources))
		matchers[key.String()] = p.matcher
	}
	apply := func(parent *EffectivePolicy, o PolicyOverrides, scope policy.Scope) *EffectivePolicy {
		p := *parent
		p.lists = maps.Clone(parent.lists)
		if o.Blocking != nil {
			p.blocking = effectiveBool{value: *o.Blocking, source: scope}
		}
		for id, val := range o.Lists {
			p.lists[id] = effectiveBool{value: val && available[id], source: scope}
		}
		if o.Upstream != nil {
			d := c.DNS
			d.Upstreams = o.Upstream.Upstreams
			d.Fallback = o.Upstream.Fallback
			p.route = internRoute(d)
			p.routeSource = scope
		}
		return &p
	}
	finish(network, "", "")
	v.network = network
	profileScopes := map[string]string{}
	for _, p := range c.Profiles {
		effective := apply(network, p.Policy, policy.Scope{Kind: policy.ProfileScope, ID: p.ID})
		effective.profileID = p.ID
		if len(p.Policy.Rules) > 0 {
			profileScopes[p.ID] = p.ID
		}
		finish(effective, profileScopes[p.ID], "")
		v.profiles[p.ID] = effective
	}
	seen4, seen6 := map[int]bool{}, map[int]bool{}
	for _, p := range c.Clients {
		parent := network
		if p.Profile != "" {
			parent = v.profiles[p.Profile]
		}
		id := clientID(p)
		effective := apply(parent, p.Overrides, policy.Scope{Kind: policy.ClientScope, ID: id})
		effective.clientID = id
		if p.PausedUntil != nil {
			effective.pausedUntil = *p.PausedUntil
		}
		scope := ""
		if len(p.Overrides.Rules) > 0 {
			scope = id
		}
		finish(effective, profileScopes[p.Profile], scope)
		v.clients[id] = effective
		if p.Address != "" {
			// Unsuitable naming-only legacy addresses retain a descriptor and
			// naming override, but cannot select policy (or create an invalid key).
			if a, err := selectorAddress(p.Address); err == nil {
				v.addresses[a] = effective
			}
		}
		for _, s := range p.Selectors.Addresses {
			a, _ := selectorAddress(s)
			v.addresses[a] = effective
		}
		for _, s := range p.Selectors.MACs {
			canonical, _ := selectorMAC(s)
			a, _ := macBytes(canonical)
			v.macs[a] = effective
		}
		for _, s := range p.Selectors.CIDRs {
			a, _ := selectorPrefix(s)
			v.prefixes[a] = effective
			if a.Addr().Is4() {
				seen4[a.Bits()] = true
			} else {
				seen6[a.Bits()] = true
			}
		}
	}
	for b := range seen4 {
		v.bits4 = append(v.bits4, b)
	}
	for b := range seen6 {
		v.bits6 = append(v.bits6, b)
	}
	slices.Sort(v.bits4)
	slices.Reverse(v.bits4)
	slices.Sort(v.bits6)
	slices.Reverse(v.bits6)
	return v, nil
}
