package control

import (
	"context"
	"encoding/json"
	"net/netip"
	"net/url"
	"time"
)

// ClientProvider is an optional extension; existing history providers need not
// implement observed naming. Configured overrides always remain in items.
type ClientProvider interface {
	Clients(context.Context, url.Values) (any, error)
}

func (s *Service) Clients(ctx context.Context, q url.Values) (any, error) {
	configured, e := s.Inspect("clients")
	if e != nil {
		return nil, e
	}
	result := configured.(map[string]any)
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
