package config

import (
	"fmt"
	"net/netip"
	"net/url"
	"strings"

	"github.com/richkeenan/dimsum/internal/clients"
	"github.com/richkeenan/dimsum/internal/lists"
	"github.com/richkeenan/dimsum/internal/localdns"
	"github.com/richkeenan/dimsum/internal/policy"
)

type CustomRule struct {
	ID      string      `yaml:"id"`
	Kind    policy.Kind `yaml:"kind"`
	Action  string      `yaml:"action"`
	Pattern string      `yaml:"pattern"`
	Enabled bool        `yaml:"enabled"`
}

// Typed text boundaries for the next local-data and naming producers.
type Record = localdns.Record
type ClientOverride = clients.Override

func (c Config) PolicyRules() []policy.Rule {
	out := c.Filtering.Rules()
	for _, r := range c.Rules {
		if r.Enabled {
			class := policy.CustomDeny
			if r.Action == "allow" {
				class = policy.CustomAllow
			}
			out = append(out, policy.Rule{ID: "custom:" + r.ID, SourceID: "custom", SourceText: r.Pattern, Kind: r.Kind, Class: class, Pattern: r.Pattern})
		}
	}
	return out
}
func validatePolicy(c Config) error {
	if err := c.Filtering.Validate(); err != nil {
		return fmt.Errorf("filtering: %w", err)
	}
	if _, err := localdns.Build(c.Zones, c.Records); err != nil {
		return fmt.Errorf("records: %w", err)
	}
	if len(c.Lists) > 64 || len(c.Rules) > 100000 {
		return fmt.Errorf("policy: at most 64 sources and 100000 custom rules")
	}
	seen := map[string]bool{}
	for i, s := range c.Lists {
		if s.ID == "" || s.ID == "custom" || seen[s.ID] {
			return fmt.Errorf("lists[%d].id: empty, reserved or duplicate", i)
		}
		seen[s.ID] = true
		u, e := url.Parse(s.URL)
		if e != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil {
			return fmt.Errorf("lists[%d].url: expected HTTP(S) URL without credentials", i)
		}
		if s.Dialect != lists.Hosts && s.Dialect != lists.Domains && s.Dialect != lists.Adblock {
			return fmt.Errorf("lists[%d].dialect: unsupported", i)
		}
		if s.DomainKind != policy.Exact && s.DomainKind != policy.Suffix {
			return fmt.Errorf("lists[%d].domain_kind: expected exact or suffix", i)
		}
		if s.Dialect == lists.Hosts && s.DomainKind != policy.Exact {
			return fmt.Errorf("lists[%d].domain_kind: hosts require exact", i)
		}
	}
	seen = map[string]bool{}
	for i, r := range c.Rules {
		if r.ID == "" || seen[r.ID] {
			return fmt.Errorf("rules[%d].id: empty or duplicate", i)
		}
		seen[r.ID] = true
		if r.Action != "allow" && r.Action != "deny" {
			return fmt.Errorf("rules[%d].action: expected allow or deny", i)
		}
		if !r.Enabled {
			if _, e := policy.Compile(0, []policy.Rule{{ID: r.ID, Kind: r.Kind, Class: policy.CustomDeny, Pattern: r.Pattern}}, policy.DefaultLimits()); e != nil {
				return fmt.Errorf("rules[%d]: %w", i, e)
			}
		}
	}
	if _, e := policy.Compile(0, c.PolicyRules(), policy.DefaultLimits()); e != nil {
		for i, r := range c.Rules {
			if strings.Contains(e.Error(), fmt.Sprintf("rule %q", "custom:"+r.ID)) {
				return fmt.Errorf("rules[%d]: %w", i, e)
			}
		}
		return fmt.Errorf("rules: %w", e)
	}
	for i, r := range c.Records {
		if _, e := policy.NormalizeName(r.Name); e != nil {
			return fmt.Errorf("records[%d].name: %w", i, e)
		}
		switch r.Type {
		case "A", "AAAA":
			a, e := netip.ParseAddr(r.Value)
			if e != nil || (r.Type == "A") != a.Is4() {
				return fmt.Errorf("records[%d].value: wrong address family", i)
			}
		case "CNAME", "PTR":
			if _, e := policy.NormalizeName(r.Value); e != nil {
				return fmt.Errorf("records[%d].value: %w", i, e)
			}
		default:
			return fmt.Errorf("records[%d].type: expected A, AAAA, CNAME or PTR", i)
		}
	}
	seen = map[string]bool{}
	for i, c := range c.Clients {
		a, e := netip.ParseAddr(c.Address)
		if e != nil || strings.TrimSpace(c.Name) == "" || seen[a.Unmap().String()] {
			return fmt.Errorf("clients[%d]: expected unique IP and nonempty name", i)
		}
		seen[a.Unmap().String()] = true
	}
	return nil
}
