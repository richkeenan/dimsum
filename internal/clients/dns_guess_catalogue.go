package clients

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
)

func (rules guessRules) scope() [32]byte {
	b, _ := json.Marshal(rules)
	return sha256.Sum256(b)
}

var defaultGuessScope = dnsGuessRules.scope()

func catalogueFor(view *View) (guessRules, [32]byte) {
	if view == nil {
		return dnsGuessRules, defaultGuessScope
	}
	return view.guesses, view.guessScope
}

// DNSGuessOverrides holds only owner changes, not a copy of the release baseline.
type DNSGuessOverrides struct {
	Custom []DNSGuessRule              `yaml:"custom,omitempty" json:"custom,omitempty"`
	Rules  map[string]DNSGuessOverride `yaml:"rules,omitempty" json:"rules,omitempty"`
}

type DNSGuessOverride struct {
	Name       *string  `yaml:"name,omitempty" json:"name,omitempty"`
	Category   *string  `yaml:"category,omitempty" json:"category,omitempty"`
	Reason     *string  `yaml:"reason,omitempty" json:"reason,omitempty"`
	Icon       *string  `yaml:"icon,omitempty" json:"icon,omitempty"`
	Disabled   *bool    `yaml:"disabled,omitempty" json:"disabled,omitempty"`
	Additions  []string `yaml:"additions,omitempty" json:"additions,omitempty"`
	Exclusions []string `yaml:"exclusions,omitempty" json:"exclusions,omitempty"`
}

func (o DNSGuessOverride) IsZero() bool {
	return o.Name == nil && o.Category == nil && o.Reason == nil && o.Icon == nil && (o.Disabled == nil || !*o.Disabled) && len(o.Additions)+len(o.Exclusions) == 0
}

func (o DNSGuessOverrides) Customized() bool {
	if len(o.Custom) > 0 {
		return true
	}
	for _, r := range o.Rules {
		if !r.IsZero() {
			return true
		}
	}
	return false
}

type DNSGuessEntry struct {
	Rule      DNSGuessRule  `json:"rule"`
	Builtin   *DNSGuessRule `json:"builtin,omitempty"`
	Origin    string        `json:"origin"`
	Enabled   bool          `json:"enabled"`
	Available bool          `json:"available"`
}

func cloneGuessRule(r DNSGuessRule) DNSGuessRule {
	r.Domains = slices.Clone(r.Domains)
	return r
}

func cloneValue[T any](v *T) *T {
	if v == nil {
		return nil
	}
	copy := *v
	return &copy
}

func (o DNSGuessOverrides) Clone() DNSGuessOverrides {
	c := DNSGuessOverrides{Custom: slices.Clone(o.Custom)}
	for i := range c.Custom {
		c.Custom[i] = cloneGuessRule(c.Custom[i])
	}
	if o.Rules != nil {
		c.Rules = make(map[string]DNSGuessOverride, len(o.Rules))
		for id, r := range o.Rules {
			r.Name, r.Category, r.Reason, r.Icon = cloneValue(r.Name), cloneValue(r.Category), cloneValue(r.Reason), cloneValue(r.Icon)
			r.Disabled = cloneValue(r.Disabled)
			r.Additions, r.Exclusions = slices.Clone(r.Additions), slices.Clone(r.Exclusions)
			c.Rules[id] = r
		}
	}
	return c
}

func DNSGuessCatalogue(o DNSGuessOverrides) ([]DNSGuessEntry, error) {
	_, entries, err := compileDNSGuessCatalogue(dnsGuessRules, o)
	return entries, err
}

func compileDNSGuessCatalogue(baseline guessRules, o DNSGuessOverrides) (guessRules, []DNSGuessEntry, error) {
	if len(o.Custom)+len(o.Rules) > 256 {
		return nil, nil, fmt.Errorf("at most 256 DNS guess customisations")
	}
	entries := map[string]DNSGuessEntry{}
	for _, rule := range baseline {
		r := cloneGuessRule(rule)
		entries[r.ID] = DNSGuessEntry{Rule: cloneGuessRule(r), Builtin: &r, Origin: "builtin", Enabled: true, Available: true}
	}
	seen := map[string]bool{}
	for _, rule := range o.Custom {
		r := cloneGuessRule(rule)
		if seen[r.ID] {
			return nil, nil, fmt.Errorf("duplicate custom rule %q", r.ID)
		}
		seen[r.ID] = true
		if err := normalizeGuessRule(&r, false); err != nil {
			return nil, nil, err
		}
		// Retain an owner's rule if a future release introduces the same ID.
		entries[r.ID] = DNSGuessEntry{Rule: r, Builtin: entries[r.ID].Builtin, Origin: "custom", Enabled: true, Available: true}
	}
	for id, override := range o.Rules {
		if !validGuessID(id) {
			return nil, nil, fmt.Errorf("invalid rule id %q", id)
		}
		if override.IsZero() {
			continue
		}
		entry, exists := entries[id]
		if !exists {
			// Preserve overrides for a removed baseline rule, including exclusions,
			// so an upgrade cannot silently reactivate a deliberately removed domain.
			entry = DNSGuessEntry{Rule: DNSGuessRule{ID: id, Name: id, Category: "unknown", Domains: []string{}}, Origin: "modified"}
		}
		if entry.Origin == "builtin" {
			entry.Origin = "modified"
		}
		r := &entry.Rule
		for _, field := range []struct{ to, from *string }{{&r.Name, override.Name}, {&r.Category, override.Category}, {&r.Reason, override.Reason}, {&r.Icon, override.Icon}} {
			if field.from != nil {
				*field.to = *field.from
			}
		}
		if override.Disabled != nil {
			entry.Enabled = entry.Available && !*override.Disabled
		}
		if len(override.Additions)+len(override.Exclusions) > 512 {
			return nil, nil, fmt.Errorf("rule %s: too many domain changes", id)
		}
		changed := map[string]bool{}
		for _, domains := range [][]string{override.Additions, override.Exclusions} {
			for _, domain := range domains {
				if !validGuessDomain(domain) || changed[domain] {
					return nil, nil, fmt.Errorf("rule %s: invalid, duplicate or overlapping domain %q", id, domain)
				}
				changed[domain] = true
			}
		}
		for _, domain := range override.Additions {
			if !slices.Contains(r.Domains, domain) {
				r.Domains = append(r.Domains, domain)
			}
		}
		r.Domains = slices.DeleteFunc(r.Domains, func(domain string) bool { return slices.Contains(override.Exclusions, domain) })
		entries[id] = entry
	}
	var active guessRules
	result := make([]DNSGuessEntry, 0, len(entries))
	total := 0
	for _, entry := range entries {
		if err := normalizeGuessRule(&entry.Rule, true); err != nil {
			return nil, nil, err
		}
		seenDomains := map[string]bool{}
		for _, domain := range entry.Rule.Domains {
			if !validGuessDomain(domain) || seenDomains[domain] {
				return nil, nil, fmt.Errorf("rule %s: invalid or duplicate domain %q", entry.Rule.ID, domain)
			}
			seenDomains[domain] = true
		}
		total += len(entry.Rule.Domains)
		if total > 4096 {
			return nil, nil, fmt.Errorf("at most 4096 DNS guess domains")
		}
		if entry.Available && entry.Enabled && len(entry.Rule.Domains) > 0 {
			active = append(active, cloneGuessRule(entry.Rule))
		}
		result = append(result, entry)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Rule.ID < result[j].Rule.ID })
	sort.Slice(active, func(i, j int) bool { return active[i].ID < active[j].ID })
	if _, err := validateGuessRules(active); err != nil {
		return nil, nil, err
	}
	return active, result, nil
}
