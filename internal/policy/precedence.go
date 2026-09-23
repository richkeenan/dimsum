package policy

import "slices"

func rank(c Class) int {
	switch c {
	case CustomAllow:
		return 0
	case CustomDeny:
		return 1
	case SubscriptionAllow:
		return 2
	case SubscriptionDeny:
		return 3
	case SpecialDeny:
		return 4
	default:
		return -1
	}
}

func moreSpecific(a, b *compiledRule) bool {
	if rank(a.rule.Class) != rank(b.rule.Class) {
		return rank(a.rule.Class) < rank(b.rule.Class)
	}
	// Only exact/suffix forms get specificity preference. More labels wins;
	// exact beats suffix on the same owner. All remaining ties use stable ID.
	score := func(r *compiledRule) int {
		if r.kind == Exact {
			return 2*len(r.labels) + 1
		}
		if r.kind == Suffix {
			return 2 * len(r.labels)
		}
		return 0
	}
	if score(a) != score(b) {
		return score(a) > score(b)
	}
	return a.rule.ID < b.rule.ID
}

// Match is name-only evaluation. It does not carry allow state between aliases.
func (m *Matcher) Match(n Name) Decision { return m.match(n, false) }

func (m *Matcher) match(n Name, explain bool) Decision {
	d := Decision{Result: Forward, Generation: m.generation}
	labels, display := n.labels(), n.Display()
	var winner *compiledRule
	for i := range m.rules {
		r := &m.rules[i]
		if !((*Selection)(nil)).eligible(r.rule.Scope, r.rule.Class, r.rule.SourceID) {
			continue
		}
		if !r.matches(n, labels, display) {
			continue
		}
		if explain && r.rule.SourceID != "" {
			d.SourceIDs = append(d.SourceIDs, r.rule.SourceID)
		}
		if winner == nil || moreSpecific(r, winner) {
			winner = r
		}
	}
	if winner != nil {
		d.RuleID = winner.rule.ID
		d.Scope = winner.rule.Scope
		d.Result = Block
		if winner.rule.Class == CustomAllow || winner.rule.Class == SubscriptionAllow {
			d.Result = Allow
		}
	}
	if explain {
		slices.Sort(d.SourceIDs)
		d.SourceIDs = slices.Compact(d.SourceIDs)
	}
	return d
}

// Evaluate applies local/pause precedence and original-question allow scope.
// Original allow must be the winning original decision: a subscription allow
// shadowed by an owner's deny must never turn into an alias-chain exemption.
func (m *Matcher) Evaluate(q Query) Decision {
	d := m.match(q.Name, q.Explain)
	if q.Local {
		d.Result = Local
		d.RuleID = ""
		d.Scope = Scope{}
		return d
	}
	if q.Paused {
		d.Result = Paused
		d.RuleID = ""
		d.Scope = Scope{}
		return d
	}
	if q.Name != q.Original {
		original := m.match(q.Original, q.Explain)
		if original.Result == Allow {
			if q.Explain {
				original.SourceIDs = append(original.SourceIDs, d.SourceIDs...)
				slices.Sort(original.SourceIDs)
				original.SourceIDs = slices.Compact(original.SourceIDs)
			}
			return original
		}
	}
	return d
}
