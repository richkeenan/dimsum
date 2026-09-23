package control

import (
	"context"
	"encoding/json"
	"net/netip"
	"net/url"
	"strconv"
	"time"

	"github.com/richkeenan/dimsum/internal/config"
)

// ClientProvider is an optional extension; existing history providers need not
// implement observed naming. Configured overrides always remain in items.
type ClientProvider interface {
	Clients(context.Context, url.Values) (any, error)
}

func (s *Service) Clients(ctx context.Context, q url.Values) (any, error) {
	status, e := s.Status()
	if e != nil {
		return nil, e
	}
	d, e := s.documentAt(status.SavedRevision)
	if e != nil {
		return map[string]any{"status": status, "configuration_error": RedactMessage(e.Error()), "observed_available": false}, nil
	}
	c := d.Config()
	configured, e := shape(c.Clients)
	if e != nil {
		return nil, e
	}
	if configured == nil {
		configured = []any{}
	}
	result := map[string]any{"status": status, "items": configured}
	snap := s.options.Store.Snapshot()
	if snap == nil {
		return nil, ErrUnavailable
	}
	if snap.Revision() != status.ActiveRevision || strconv.FormatUint(snap.Generation(), 10) != status.ActiveGeneration {
		return nil, config.ErrConflict
	}
	view, e := snap.PreviewClientPolicies(c)
	if e != nil {
		return nil, e
	}
	now := time.Now()
	saved := compactClientSummaries(c, view, status, now)
	active := compactClientSummaries(snap.Config(), snap.ClientPolicies(), status, now)
	summaries := make(map[string]ClientInventoryPolicy, len(saved))
	for id, desired := range saved {
		summaries[id] = ClientInventoryPolicy{Desired: desired, Active: active[id]}
	}
	result["policy_summaries"] = summaries
	items, _ := result["items"].([]any)
	for _, item := range items {
		row, ok := item.(map[string]any)
		if !ok {
			continue
		}
		id, _ := row["id"].(string)
		if id == "" {
			raw, _ := row["address"].(string)
			if address, err := netip.ParseAddr(raw); err == nil {
				id = "address:" + address.Unmap().String()
			}
		}
		row["policy_id"] = id
	}
	result["observed_available"] = false
	provider, ok := s.options.Provider.(ClientProvider)
	if !ok {
		return result, nil
	}
	observed, e := provider.Clients(ctx, q)
	if e != nil {
		return nil, e
	}
	result["observed_available"] = true
	result["observed"], e = s.observedPolicyIdentity(observed)
	if e != nil {
		return nil, e
	}
	return result, nil
}

// Compact inventory descriptors deliberately omit rules, routes and list maps.
// Each configuration is compiled once per request, never once per device.
type ClientPolicySummary struct {
	ProfileID         string `json:"profile_id"`
	OverrideCount     int    `json:"override_count"`
	Blocking          bool   `json:"blocking"`
	Filtering         bool   `json:"filtering"`
	GlobalPaused      bool   `json:"global_paused"`
	SourceUnavailable bool   `json:"source_unavailable"`
}
type ClientInventoryPolicy struct {
	Desired *ClientPolicySummary `json:"desired"`
	Active  *ClientPolicySummary `json:"active"`
}

func compactClientSummaries(c config.Config, view *config.ClientPolicies, status Activation, now time.Time) map[string]*ClientPolicySummary {
	result := make(map[string]*ClientPolicySummary, len(c.Clients))
	for _, client := range c.Clients {
		id := policyClientID(client)
		p, ok := view.Client(id)
		if !ok {
			continue
		}
		blocking, _ := p.Blocking()
		n := overrideCount(client.Overrides)
		if client.PausedUntil != nil {
			n++
		}
		v := &ClientPolicySummary{ProfileID: p.ProfileID(), OverrideCount: n, Blocking: blocking, GlobalPaused: c.Filtering.Paused(now)}
		v.Filtering = blocking && !v.GlobalPaused && !now.Before(p.PausedUntil())
		for _, source := range status.Sources {
			enabled, _ := p.List(source.ID)
			if enabled && source.Enabled && !source.Usable {
				v.SourceUnavailable = true
				break
			}
		}
		result[id] = v
	}
	return result
}

func (s *Service) observedPolicyIdentity(observed any) (map[string]any, error) {
	b, err := json.Marshal(observed)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err = json.Unmarshal(b, &result); err != nil {
		return nil, err
	}
	if s.options.Store == nil || s.options.Store.Snapshot() == nil {
		return result, nil
	}
	snap := s.options.Store.Snapshot()
	c := snap.Config()
	now := time.Now()
	items, _ := result["items"].([]any)
	for _, item := range items {
		row, ok := item.(map[string]any)
		if !ok {
			continue
		}
		raw, _ := row["address"].(string)
		address, err := netip.ParseAddr(raw)
		if err != nil {
			continue
		}
		mac := s.authoritativeMAC(address, snap, now)
		p := snap.ClientPolicies().Select(address, mac)
		row["client_id"] = p.ClientID()
		row["matching_method"] = matchingMethod(c, p, address, mac)
		if mac != "" {
			row["authoritative_mac"] = mac
		} else {
			delete(row, "authoritative_mac")
		}
	}
	return result, nil
}
