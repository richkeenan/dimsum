package config

import (
	"context"
	"slices"

	"github.com/richkeenan/dimsum/internal/lists"
	"github.com/richkeenan/dimsum/internal/policy"
)

// recoverBuiltins replaces release-coupled memberships before publication while
// retaining external feeds from recovery, without requiring their source caches
// or network access. A binary upgrade or rollback is an intentional list update.
func (s *Store) recoverBuiltins(c Config, old *subscriptionState) (*subscriptionState, bool, error) {
	updated := map[string]lists.Version{}
	statuses := slices.Clone(old.sources)
	for i, sub := range c.Lists {
		if !sub.Enabled || sub.URL != lists.WorkCompatibilityURL {
			continue
		}
		var fetcher *lists.Fetcher
		v, err := fetcher.Refresh(context.Background(), "", sub, true)
		if err != nil {
			return nil, false, err
		}
		if statuses[i].Usable && statuses[i].SHA256 == v.SHA256 && statuses[i].Error == v.Warning {
			continue
		}
		updated[sub.ID] = v
		statuses[i] = SourceStatus{ID: sub.ID, Enabled: true, Usable: true, Rules: len(v.Rules), SHA256: v.SHA256, Error: v.Warning}
	}
	if len(updated) == 0 {
		return old, false, nil
	}
	var rules []policy.Rule
	for number := uint32(1); ; number++ {
		rule, ok := old.policy.RuleAt(number)
		if !ok {
			break
		}
		if _, replaced := updated[rule.SourceID]; !replaced {
			rules = append(rules, rule)
		}
	}
	for _, sub := range c.Lists {
		rules = append(rules, updated[sub.ID].Rules...)
	}
	next, err := s.compileSubscriptions(c, rules, statuses)
	return next, true, err
}
