package control

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"reflect"
	"strconv"
	"time"

	"github.com/richkeenan/dimsum/internal/config"
	"github.com/richkeenan/dimsum/internal/dhcp"
	"github.com/richkeenan/dimsum/internal/lists"
	"github.com/richkeenan/dimsum/internal/policy"
)

// ClientPolicyMutation targets configured identity, never a display name.
// Fields are relative sparse policy paths; resets remove YAML keys.
type ClientPolicyMutation struct {
	Revision     string                  `json:"revision"`
	Scope        string                  `json:"scope"`
	ID           string                  `json:"id,omitempty"`
	Create       bool                    `json:"create,omitempty"`
	Delete       bool                    `json:"delete,omitempty"`
	PromoteID    string                  `json:"promote_id,omitempty"`
	Name         string                  `json:"name,omitempty"`
	Selectors    *config.ClientSelectors `json:"selectors,omitempty"`
	LeaseAddress string                  `json:"lease_address,omitempty"`
	Profile      *string                 `json:"profile,omitempty"`
	PausedUntil  *time.Time              `json:"paused_until,omitempty"`
	ResetPause   bool                    `json:"reset_pause,omitempty"`
	ResetAll     bool                    `json:"reset_all,omitempty"`
	Fields       []config.PolicyField    `json:"fields,omitempty"`
	Subscribe    []PolicySubscription    `json:"subscribe,omitempty"`
}

type PolicySubscription struct {
	ID         string        `json:"id"`
	URL        string        `json:"url"`
	Dialect    lists.Dialect `json:"dialect"`
	DomainKind policy.Kind   `json:"domain_kind"`
	Enabled    bool          `json:"enabled"`
}

func policyClientID(c config.ClientOverride) string {
	if c.ID != "" {
		return c.ID
	}
	a, _ := netip.ParseAddr(c.Address)
	return "address:" + a.Unmap().String()
}

func (s *Service) authoritativeMAC(address netip.Addr, snap *config.Snapshot, now time.Time) string {
	if s.options.Leases != nil && snap != nil {
		leases := s.options.Leases(snap)
		if leases != nil && leases.Generation() == snap.Generation() {
			return leases.AuthoritativeMAC(address, now)
		}
		return ""
	}
	if s.options.DHCPInspect == nil || snap == nil {
		return ""
	}
	leases := s.options.DHCPInspect()
	if leases.Generation != snap.Generation() {
		return ""
	}
	for _, l := range leases.Leases {
		if l.Address.Unmap() == address.Unmap() && l.State == dhcp.Bound && now.Before(l.Expiry) && l.MAC != [6]byte{} && l.MAC[0]&1 == 0 {
			return net.HardwareAddr(l.MAC[:]).String()
		}
	}
	return ""
}

func (s *Service) clientPolicyCandidate(m ClientPolicyMutation) (*config.Document, string, error) {
	if s.options.Store == nil {
		return nil, "", ErrUnavailable
	}
	if m.Revision == "" {
		return nil, "", fmt.Errorf("revision is required")
	}
	d, err := s.documentAt(m.Revision)
	if err != nil {
		return nil, "", err
	}
	if len(m.Fields) > 128 || len(m.Subscribe) > 64 {
		return nil, "", fmt.Errorf("too many policy changes")
	}
	c := d.Config()
	id := m.ID
	var base, owner []string
	index := -1
	switch m.Scope {
	case "network":
		if id != "" || m.Create || m.Delete || m.PromoteID != "" || m.Name != "" || m.Selectors != nil || m.LeaseAddress != "" || m.Profile != nil || m.PausedUntil != nil || m.ResetPause {
			return nil, "", fmt.Errorf("network policy has no device identity")
		}
	case "client":
		for i, v := range c.Clients {
			if policyClientID(v) == id {
				index = i
			}
		}
		owner = []string{"clients", strconv.Itoa(index)}
		base = append(append([]string{}, owner...), "overrides")
	case "profile":
		if m.Selectors != nil || m.LeaseAddress != "" || m.Profile != nil || m.PausedUntil != nil || m.ResetPause || m.PromoteID != "" {
			return nil, "", fmt.Errorf("profile cannot have device identity fields")
		}
		for i, v := range c.Profiles {
			if v.ID == id {
				index = i
			}
		}
		owner = []string{"profiles", strconv.Itoa(index)}
		base = append(append([]string{}, owner...), "policy")
	default:
		return nil, "", fmt.Errorf("scope must be network, profile, or client")
	}
	if m.Scope != "network" && id == "" {
		return nil, "", fmt.Errorf("explicit stable id required; inspect clients and select one, never guess from a name")
	}
	if m.Delete {
		if m.Create || m.ResetAll || len(m.Fields) > 0 || len(m.Subscribe) > 0 || m.Name != "" || m.Selectors != nil || m.LeaseAddress != "" || m.Profile != nil || m.PausedUntil != nil || m.ResetPause || m.PromoteID != "" {
			return nil, "", fmt.Errorf("delete cannot be combined with other changes")
		}
		if index < 0 {
			return nil, "", NotFound
		}
		next, e := d.Remove(owner)
		return next, id, e
	}
	if m.Create && index >= 0 {
		return nil, "", fmt.Errorf("id already exists")
	}
	if m.Create && m.PromoteID != "" {
		return nil, "", fmt.Errorf("promote_id is only for an existing legacy client")
	}
	if !m.Create && m.Scope != "network" && index < 0 {
		return nil, "", NotFound
	}
	selectors := m.Selectors
	if m.LeaseAddress != "" {
		if selectors != nil {
			return nil, "", fmt.Errorf("lease_address and selectors are mutually exclusive")
		}
		a, e := netip.ParseAddr(m.LeaseAddress)
		if e != nil {
			return nil, "", e
		}
		mac := s.authoritativeMAC(a, s.options.Store.Snapshot(), time.Now())
		if mac == "" {
			return nil, "", fmt.Errorf("no authoritative unexpired DHCP lease; select explicit selectors")
		}
		selectors = &config.ClientSelectors{MACs: []string{mac}}
	}
	var fields []config.PolicyField
	for _, sub := range m.Subscribe {
		found := false
		for i, existing := range c.Lists {
			if existing.ID == sub.ID {
				found = true
				if existing.URL != sub.URL || existing.Dialect != sub.Dialect || existing.DomainKind != sub.DomainKind {
					return nil, "", fmt.Errorf("subscription %q already exists with different source", sub.ID)
				}
				if sub.Enabled && !existing.Enabled {
					// Selecting a disabled source can resume its downloads without
					// changing which devices inherit it, including legacy defaults.
					fields = append(fields, config.PolicyField{Path: []string{"lists", strconv.Itoa(i), "enabled"}, Value: true})
					if existing.DefaultApply == nil {
						fields = append(fields, config.PolicyField{Path: []string{"lists", strconv.Itoa(i), "default_apply"}, Value: false})
					}
				}
			}
		}
		if !found {
			no := false
			value := lists.Subscription{ID: sub.ID, URL: sub.URL, Dialect: sub.Dialect, DomainKind: sub.DomainKind, Enabled: sub.Enabled, DefaultApply: &no}
			fields = append(fields, config.PolicyField{Path: []string{"lists"}, Value: value, Append: true})
			c.Lists = append(c.Lists, value)
		}
	}
	if m.Create {
		var item any
		if m.Scope == "client" {
			index = len(c.Clients)
			v := config.ClientOverride{ID: id, Name: m.Name}
			if selectors != nil {
				v.Selectors = *selectors
			}
			item = v
		} else {
			index = len(c.Profiles)
			item = config.Profile{ID: id, Name: m.Name}
		}
		owner[1] = strconv.Itoa(index)
		base[1] = owner[1]
		fields = append(fields, config.PolicyField{Path: owner[:1], Value: item, Append: true})
	} else if m.Scope == "client" && c.Clients[index].ID == "" {
		if m.PromoteID == "" {
			return nil, "", fmt.Errorf("legacy client requires promote_id on explicit policy save")
		}
		id = m.PromoteID
		fields = append(fields, config.PolicyField{Path: appendPath(owner, "id"), Value: id})
	} else if m.PromoteID != "" {
		return nil, "", fmt.Errorf("stable IDs cannot be changed")
	}
	if !m.Create && m.Name != "" {
		fields = append(fields, config.PolicyField{Path: appendPath(owner, "name"), Value: m.Name})
	}
	if !m.Create && selectors != nil {
		fields = append(fields, config.PolicyField{Path: appendPath(owner, "address"), Reset: true}, config.PolicyField{Path: appendPath(owner, "selectors"), Value: *selectors})
	}
	if m.Profile != nil {
		if *m.Profile == "" {
			fields = append(fields, config.PolicyField{Path: appendPath(owner, "profile"), Reset: true})
		} else {
			fields = append(fields, config.PolicyField{Path: appendPath(owner, "profile"), Value: *m.Profile})
		}
	}
	if m.PausedUntil != nil && m.ResetPause {
		return nil, "", fmt.Errorf("pause and reset_pause are mutually exclusive")
	}
	if m.PausedUntil != nil {
		fields = append(fields, config.PolicyField{Path: appendPath(owner, "paused_until"), Value: m.PausedUntil.UTC().Format(time.RFC3339Nano)})
	}
	if m.ResetPause {
		fields = append(fields, config.PolicyField{Path: appendPath(owner, "paused_until"), Reset: true})
	}
	if m.ResetAll {
		if len(m.Fields) > 0 {
			return nil, "", fmt.Errorf("reset_all cannot be combined with fields")
		}
		if m.Scope == "network" {
			return nil, "", fmt.Errorf("reset_all requires a client or profile")
		}
		fields = append(fields, config.PolicyField{Path: base, Reset: true})
	}
	for _, f := range m.Fields {
		// Validate the original operation before network route expansion, which
		// otherwise discards Value when translating one reset into two removals.
		if f.Reset && f.Value != nil || !f.Reset && f.Value == nil {
			return nil, "", fmt.Errorf("policy field requires either a value or reset:true")
		}
		if f.Append || len(f.Path) < 1 || len(f.Path) > 2 {
			return nil, "", fmt.Errorf("unsupported policy field")
		}
		key := f.Path[0]
		if !(len(f.Path) == 1 && (key == "blocking" || key == "upstream" || key == "rules") || len(f.Path) == 2 && key == "lists") {
			return nil, "", fmt.Errorf("unsupported policy field %v", f.Path)
		}
		value, e := normalizeItem(f.Value)
		if e != nil {
			return nil, "", e
		}
		f.Value = value
		if m.Scope == "network" {
			switch key {
			case "lists":
				listIndex := -1
				for i, l := range c.Lists {
					if l.ID == f.Path[1] {
						listIndex = i
					}
				}
				if listIndex < 0 {
					return nil, "", fmt.Errorf("unknown subscription")
				}
				f.Path = []string{"lists", strconv.Itoa(listIndex), "default_apply"}
			case "upstream":
				if f.Reset {
					fields = append(fields, config.PolicyField{Path: []string{"dns", "upstreams"}, Reset: true}, config.PolicyField{Path: []string{"dns", "fallback_upstreams"}, Reset: true})
				} else {
					b, e := json.Marshal(f.Value)
					if e != nil {
						return nil, "", e
					}
					var route config.UpstreamRoute
					decoder := json.NewDecoder(bytes.NewReader(b))
					decoder.DisallowUnknownFields()
					if e = decoder.Decode(&route); e != nil || len(route.Upstreams) == 0 {
						return nil, "", fmt.Errorf("upstream requires nonempty upstreams and optional fallback_upstreams")
					}
					fields = append(fields, config.PolicyField{Path: []string{"dns", "upstreams"}, Value: route.Upstreams})
					if len(route.Fallback) == 0 {
						fields = append(fields, config.PolicyField{Path: []string{"dns", "fallback_upstreams"}, Reset: true})
					} else {
						fields = append(fields, config.PolicyField{Path: []string{"dns", "fallback_upstreams"}, Value: route.Fallback})
					}
				}
				continue
			}
		} else {
			f.Path = append(append([]string{}, base...), f.Path...)
		}
		fields = append(fields, f)
	}
	if len(fields) == 0 {
		return nil, "", fmt.Errorf("no changes supplied")
	}
	next, e := d.PolicyFields(fields)
	return next, id, e
}
func appendPath(base []string, key string) []string { return append(append([]string{}, base...), key) }

func (s *Service) MutateClientPolicy(ctx context.Context, m ClientPolicyMutation) (Activation, error) {
	d, _, err := s.clientPolicyCandidate(m)
	if err != nil {
		return Activation{}, err
	}
	if err = s.checkDHCPAvailability(d); err != nil {
		return Activation{}, err
	}
	a, err := s.options.Store.Save(ctx, m.Revision, d)
	return activation(a), err
}

type EffectiveBool struct {
	Value  bool         `json:"value"`
	Source policy.Scope `json:"source"`
}
type EffectiveRule struct {
	Rule   any          `json:"rule"`
	Source policy.Scope `json:"source"`
}
type EffectiveClientPolicy struct {
	ID             string                   `json:"id"`
	ProfileID      string                   `json:"profile_id"`
	Blocking       EffectiveBool            `json:"blocking"`
	Filtering      bool                     `json:"filtering"`
	PausedUntil    time.Time                `json:"paused_until"`
	GlobalPaused   bool                     `json:"global_paused"`
	Lists          map[string]EffectiveBool `json:"lists"`
	UpstreamSource policy.Scope             `json:"upstream_source"`
	RouteID        string                   `json:"route_id"`
	Upstream       config.UpstreamRoute     `json:"upstream"`
	OverrideCount  int                      `json:"override_count"`
	Rules          []EffectiveRule          `json:"rules"`
}

func effectivePolicy(c config.Config, p *config.EffectivePolicy, now time.Time) EffectiveClientPolicy {
	b, source := p.Blocking()
	v := EffectiveClientPolicy{ID: p.ClientID(), ProfileID: p.ProfileID(), Blocking: EffectiveBool{b, source}, PausedUntil: p.PausedUntil(), GlobalPaused: c.Filtering.Paused(now), Lists: map[string]EffectiveBool{}, UpstreamSource: p.UpstreamSource(), RouteID: p.RouteID()}
	v.Filtering = b && !now.Before(v.PausedUntil) && !v.GlobalPaused
	v.Rules = []EffectiveRule{}
	addRules := func(rules []config.CustomRule, source policy.Scope) {
		for _, r := range rules {
			value, _ := shape(r)
			v.Rules = append(v.Rules, EffectiveRule{value, source})
		}
	}
	addRules(c.Rules, policy.Scope{})
	for _, l := range c.Lists {
		enabled, scope := p.List(l.ID)
		v.Lists[l.ID] = EffectiveBool{enabled, scope}
	}
	// Configured route strings remain suitable for editing; transport options are
	// deliberately not serialized (they include implementation-only fields).
	v.Upstream = config.UpstreamRoute{Upstreams: c.DNS.Upstreams, Fallback: c.DNS.Fallback}
	if v.Upstream.Upstreams == nil {
		v.Upstream.Upstreams = []string{}
	}
	for _, profile := range c.Profiles {
		if profile.ID == p.ProfileID() {
			addRules(profile.Policy.Rules, policy.Scope{Kind: policy.ProfileScope, ID: profile.ID})
			if p.ClientID() == "" {
				v.OverrideCount = overrideCount(profile.Policy)
			}
		}
		if profile.ID == p.ProfileID() && profile.Policy.Upstream != nil {
			v.Upstream = *profile.Policy.Upstream
		}
	}
	for _, client := range c.Clients {
		if policyClientID(client) == p.ClientID() {
			addRules(client.Overrides.Rules, policy.Scope{Kind: policy.ClientScope, ID: p.ClientID()})
			o := client.Overrides
			v.OverrideCount = len(o.Lists) + len(o.Rules)
			if o.Blocking != nil {
				v.OverrideCount++
			}
			if o.Upstream != nil {
				v.OverrideCount++
				v.Upstream = *o.Upstream
			}
			if client.PausedUntil != nil {
				v.OverrideCount++
			}
		}
	}
	return v
}
func overrideCount(o config.PolicyOverrides) int {
	n := len(o.Lists) + len(o.Rules)
	if o.Blocking != nil {
		n++
	}
	if o.Upstream != nil {
		n++
	}
	return n
}
func selectPolicy(v *config.ClientPolicies, scope, id string) (*config.EffectivePolicy, error) {
	switch scope {
	case "network":
		if id == "" {
			return v.Network(), nil
		}
	case "client":
		if p, ok := v.Client(id); ok {
			return p, nil
		}
	case "profile":
		if p, ok := v.Profile(id); ok {
			return p, nil
		}
	}
	return nil, NotFound
}

type ClientPolicyRead struct {
	Status    Activation             `json:"status"`
	Scope     string                 `json:"scope"`
	ID        string                 `json:"id"`
	Desired   any                    `json:"desired"`
	Effective EffectiveClientPolicy  `json:"effective"`
	Active    *EffectiveClientPolicy `json:"active"`
}

func (s *Service) ReadClientPolicy(scope, id string) (ClientPolicyRead, error) {
	status, err := s.Status()
	if err != nil {
		return ClientPolicyRead{}, err
	}
	d, err := s.documentAt(status.SavedRevision)
	if err != nil {
		return ClientPolicyRead{}, err
	}
	snap := s.options.Store.Snapshot()
	if snap == nil {
		return ClientPolicyRead{}, ErrUnavailable
	}
	if snap.Revision() != status.ActiveRevision || strconv.FormatUint(snap.Generation(), 10) != status.ActiveGeneration {
		return ClientPolicyRead{}, config.ErrConflict
	}
	views, err := snap.PreviewClientPolicies(d.Config())
	if err != nil {
		return ClientPolicyRead{}, err
	}
	p, err := selectPolicy(views, scope, id)
	if err != nil {
		return ClientPolicyRead{}, err
	}
	c := d.Config()
	var desired any
	switch scope {
	case "client":
		for _, v := range c.Clients {
			if policyClientID(v) == id {
				desired, _ = shape(v)
			}
		}
	case "profile":
		for _, v := range c.Profiles {
			if v.ID == id {
				desired, _ = shape(v)
			}
		}
	case "network":
		defaults := map[string]bool{}
		for _, l := range c.Lists {
			if l.DefaultApply != nil {
				defaults[l.ID] = *l.DefaultApply
			}
		}
		rules := []any{}
		for _, r := range c.Rules {
			value, _ := shape(r)
			rules = append(rules, value)
		}
		value := map[string]any{"lists": defaults, "upstream": effectivePolicy(c, p, time.Now()).Upstream, "rules": rules}
		if c.Blocking != nil {
			value["blocking"] = *c.Blocking
		}
		desired = value
	}
	result := ClientPolicyRead{Status: status, Scope: scope, ID: id, Desired: desired, Effective: effectivePolicy(c, p, time.Now())}
	if active, e := selectPolicy(snap.ClientPolicies(), scope, id); e == nil {
		v := effectivePolicy(snap.Config(), active, time.Now())
		result.Active = &v
	}
	return result, nil
}

type ClientPolicyPreview struct {
	Revision         string                 `json:"revision"`
	ID               string                 `json:"id"`
	Effective        *EffectiveClientPolicy `json:"effective"`
	ChangedClients   []string               `json:"changed_clients"`
	NetworkChanged   bool                   `json:"network_changed"`
	DownloadsPending []string               `json:"downloads_pending"`
}

func (s *Service) PreviewClientPolicy(m ClientPolicyMutation) (ClientPolicyPreview, error) {
	d, id, err := s.clientPolicyCandidate(m)
	if err != nil {
		return ClientPolicyPreview{}, err
	}
	if err = s.checkDHCPAvailability(d); err != nil {
		return ClientPolicyPreview{}, err
	}
	old, err := s.documentAt(m.Revision)
	if err != nil {
		return ClientPolicyPreview{}, err
	}
	snap := s.options.Store.Snapshot()
	if snap == nil {
		return ClientPolicyPreview{}, ErrUnavailable
	}
	oldConfig, newConfig := old.Config(), d.Config()
	before, err := snap.PreviewClientPolicies(oldConfig)
	if err != nil {
		return ClientPolicyPreview{}, err
	}
	after, err := snap.PreviewClientPolicies(newConfig)
	if err != nil {
		return ClientPolicyPreview{}, err
	}
	now := time.Now()
	result := ClientPolicyPreview{Revision: m.Revision, ID: id, ChangedClients: []string{}, DownloadsPending: []string{}}
	if p, e := selectPolicy(after, m.Scope, id); e == nil {
		v := effectivePolicy(newConfig, p, now)
		result.Effective = &v
	}
	listIDs := map[string]bool{}
	for _, l := range oldConfig.Lists {
		listIDs[l.ID] = true
	}
	for _, l := range newConfig.Lists {
		listIDs[l.ID] = true
	}
	rulesChanged := !reflect.DeepEqual(oldConfig.Rules, newConfig.Rules)
	oldProfiles := map[string]config.Profile{}
	for _, p := range oldConfig.Profiles {
		oldProfiles[p.ID] = p
	}
	profileRulesChanged := map[string]bool{}
	for _, p := range newConfig.Profiles {
		profileRulesChanged[p.ID] = !reflect.DeepEqual(oldProfiles[p.ID].Policy.Rules, p.Policy.Rules)
	}
	result.NetworkChanged = rulesChanged || !samePolicyDescriptor(before.Network(), after.Network(), listIDs)
	oldClients := map[string]config.ClientOverride{}
	for _, c := range oldConfig.Clients {
		oldClients[policyClientID(c)] = c
	}
	for _, c := range newConfig.Clients {
		key := policyClientID(c)
		a, _ := after.Client(key)
		b, ok := before.Client(key)
		if !ok || rulesChanged || profileRulesChanged[a.ProfileID()] || !reflect.DeepEqual(oldClients[key], c) || !samePolicyDescriptor(b, a, listIDs) {
			result.ChangedClients = append(result.ChangedClients, key)
		}
	}
	for _, c := range oldConfig.Clients {
		key := policyClientID(c)
		if _, ok := after.Client(key); !ok {
			result.ChangedClients = append(result.ChangedClients, key)
		}
	}
	activeSources := map[string]lists.Subscription{}
	for _, l := range snap.Config().Lists {
		activeSources[l.ID] = l
	}
	statuses := s.options.Store.Inspect().Sources
	for _, l := range newConfig.Lists {
		found := false
		active := activeSources[l.ID]
		for _, status := range statuses {
			if status.ID == l.ID && status.Usable && active.URL == l.URL && active.Dialect == l.Dialect && active.DomainKind == l.DomainKind && active.Enabled == l.Enabled {
				found = true
			}
		}
		if l.Enabled && !found {
			result.DownloadsPending = append(result.DownloadsPending, l.ID)
		}
	}
	return result, nil
}

// Compare compact compiled descriptors without serializing every inherited rule
// for every client. Subscription membership remains shared by the compiler.
func samePolicyDescriptor(a, b *config.EffectivePolicy, lists map[string]bool) bool {
	av, as := a.Blocking()
	bv, bs := b.Blocking()
	if av != bv || as != bs || a.ProfileID() != b.ProfileID() || a.RouteID() != b.RouteID() || a.UpstreamSource() != b.UpstreamSource() || !a.PausedUntil().Equal(b.PausedUntil()) {
		return false
	}
	for id := range lists {
		av, as := a.List(id)
		bv, bs := b.List(id)
		if av != bv || as != bs {
			return false
		}
	}
	return true
}
