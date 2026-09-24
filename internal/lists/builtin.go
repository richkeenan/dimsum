package lists

import (
	_ "embed"
	"fmt"
	"sort"
	"strings"

	"github.com/richkeenan/dimsum/internal/policy"
)

// WorkCompatibilityURL identifies the independently maintained allowlist shipped
// with dimsum. Its baseline changes only when the executable is updated.
const WorkCompatibilityURL = "builtin://work-compatibility"

//go:embed builtin/work-compatibility.adblock
var workCompatibility string

// BuiltinOverrides stores only owner changes, never a copy of the release baseline.
type BuiltinOverrides struct {
	Additions  []string `yaml:"additions,omitempty" json:"additions,omitempty"`
	Exclusions []string `yaml:"exclusions,omitempty" json:"exclusions,omitempty"`
}

type BuiltinEntry struct {
	Domain  string `json:"domain"`
	Origin  string `json:"origin"`
	Removed bool   `json:"removed"`
}

func NormalizeBuiltinDomain(domain string) (string, error) {
	rule, code := domainRule(domain, policy.Suffix)
	if code != "" {
		return "", fmt.Errorf("expected a plain domain: %s", code)
	}
	return rule.Pattern, nil
}

func ValidateBuiltinOverrides(s Subscription) error {
	if s.BuiltinOverrides == nil {
		return nil
	}
	if s.URL != WorkCompatibilityURL {
		return fmt.Errorf("builtin_overrides requires a known built-in source")
	}
	o := s.BuiltinOverrides
	if len(o.Additions)+len(o.Exclusions) > 10000 {
		return fmt.Errorf("at most 10000 built-in customizations")
	}
	seen := map[string]bool{}
	for _, domains := range [][]string{o.Additions, o.Exclusions} {
		for _, domain := range domains {
			n, err := NormalizeBuiltinDomain(domain)
			if err != nil || n != domain {
				return fmt.Errorf("builtin_overrides: expected canonical domain %q", domain)
			}
			if seen[n] {
				return fmt.Errorf("builtin_overrides: duplicate or overlapping domain %q", domain)
			}
			seen[n] = true
		}
	}
	return nil
}

func BuiltinEntries(s Subscription) ([]BuiltinEntry, error) {
	if s.URL != WorkCompatibilityURL {
		return nil, fmt.Errorf("not a known built-in source")
	}
	if err := ValidateBuiltinOverrides(s); err != nil {
		return nil, err
	}
	parsed, err := Parse(strings.NewReader(workCompatibility), Source{ID: s.ID, Dialect: Adblock, DomainKind: policy.Suffix}, DefaultLimits())
	if err != nil {
		return nil, err
	}
	entries := map[string]BuiltinEntry{}
	for _, r := range parsed.Rules {
		entries[r.Pattern] = BuiltinEntry{Domain: r.Pattern, Origin: "builtin"}
	}
	if o := s.BuiltinOverrides; o != nil {
		for _, domain := range o.Additions {
			if _, ok := entries[domain]; !ok {
				entries[domain] = BuiltinEntry{Domain: domain, Origin: "custom"}
			}
		}
		for _, domain := range o.Exclusions {
			entry, ok := entries[domain]
			// Retain exclusions absent from this release so upgrades cannot silently restore them.
			if !ok {
				entry = BuiltinEntry{Domain: domain, Origin: "builtin"}
			}
			entry.Removed = true
			entries[domain] = entry
		}
	}
	out := make([]BuiltinEntry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Domain < out[j].Domain })
	return out, nil
}

func builtinText(s Subscription) (string, error) {
	entries, err := BuiltinEntries(s)
	if err != nil {
		return "", err
	}
	var text strings.Builder
	for _, entry := range entries {
		if !entry.Removed {
			fmt.Fprintf(&text, "@@||%s^\n", entry.Domain)
		}
	}
	return text.String(), nil
}
