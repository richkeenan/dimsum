package policy

import (
	"fmt"
	"math"
	"slices"
	"strings"
)

// Admission summaries are computed once, while compiling each flat component.
// In particular, a custom-rule edit never walks subscription rules or regexes.
type snapshotResources struct {
	inputBytes                                             uint64
	regexBytes                                             int64
	regexCount, maxExpressionBytes, maxProgramInstructions int
	classes, namespaces                                    uint8
}

const (
	customNamespace uint8 = 1 << iota
	specialNamespace
	subscriptionNamespace
)

func ruleNamespace(id string) uint8 {
	if strings.HasPrefix(id, "custom:") || strings.HasPrefix(id, "scoped-custom:") {
		return customNamespace
	}
	if strings.HasPrefix(id, "special:") {
		return specialNamespace
	}
	return subscriptionNamespace
}

// CompileOverlay compiles only owner rules and shares the immutable subscription
// indexes. Owner IDs must use custom: or scoped-custom: (custom allow/deny) or
// special: (special deny); subscriptions must use none of these prefixes and
// only subscription classes. Scoped IDs are disjoint from arbitrary legacy
// network IDs, which always begin with custom:.
// Nested overlays and nil bases are rejected. Numeric IDs are generation-local:
// owner entries precede subscription entries in their original compilation order.
func CompileOverlay(generation uint64, ownerRules []Rule, subscriptions *PolicySnapshot, limits Limits) (*PolicySnapshot, error) {
	return compileOverlay(generation, ownerRules, subscriptions, limits, DefaultSnapshotOptions())
}

func compileOverlay(generation uint64, ownerRules []Rule, base *PolicySnapshot, limits Limits, options SnapshotOptions) (*PolicySnapshot, error) {
	if base == nil || base.base != nil {
		return nil, fmt.Errorf("policy: overlay requires a flat subscription snapshot")
	}
	if base.resources.classes & ^uint8((1<<2)|(1<<3)) != 0 || base.resources.namespaces & ^subscriptionNamespace != 0 {
		return nil, fmt.Errorf("policy: invalid subscription classes or ID namespaces")
	}
	for _, r := range ownerRules {
		if !((r.Class == CustomAllow || r.Class == CustomDeny) && ruleNamespace(r.ID) == customNamespace || r.Class == SpecialDeny && ruleNamespace(r.ID) == specialNamespace) {
			return nil, fmt.Errorf("policy: invalid owner class or ID namespace for %q", r.ID)
		}
	}
	owner, err := CompileSnapshotWithOptions(generation, ownerRules, limits, options)
	if err != nil {
		return nil, err
	}
	a, b := owner.resources, base.resources
	if b.regexCount > limits.MaxRegex-a.regexCount || b.regexBytes > limits.MaxTotalRegexBytes-a.regexBytes || b.maxExpressionBytes > limits.MaxExpressionBytes || b.maxProgramInstructions > limits.MaxProgramInstructions {
		return nil, fmt.Errorf("policy: aggregate overlay regex budget exceeded")
	}
	if base.memory.Rules > options.MaxRules-owner.memory.Rules || uint64(base.memory.Rules)+uint64(owner.memory.Rules) > math.MaxUint32-1 || b.inputBytes > options.MaxBytes-a.inputBytes {
		return nil, fmt.Errorf("policy: aggregate overlay input budget exceeded")
	}
	if base.memory.TotalBytes > options.MaxBytes-owner.memory.TotalBytes {
		return nil, fmt.Errorf("policy: aggregate overlay byte budget exceeded")
	}
	owner.base = base
	owner.resources = snapshotResources{
		inputBytes: a.inputBytes + b.inputBytes, regexBytes: a.regexBytes + b.regexBytes,
		regexCount:             a.regexCount + b.regexCount,
		maxExpressionBytes:     max(a.maxExpressionBytes, b.maxExpressionBytes),
		maxProgramInstructions: max(a.maxProgramInstructions, b.maxProgramInstructions),
		classes:                a.classes | b.classes, namespaces: a.namespaces | b.namespaces,
	}
	owner.memory = SnapshotMemory{
		Rules:           owner.memory.Rules + base.memory.Rules,
		TotalBytes:      owner.memory.TotalBytes + base.memory.TotalBytes,
		ProvenanceBytes: owner.memory.ProvenanceBytes + base.memory.ProvenanceBytes,
		ExactBytes:      owner.memory.ExactBytes + base.memory.ExactBytes,
		SuffixBytes:     owner.memory.SuffixBytes + base.memory.SuffixBytes,
		FallbackBytes:   owner.memory.FallbackBytes + base.memory.FallbackBytes,
	}
	return owner, nil
}

func (s *PolicySnapshot) matchOverlay(n Name, explain bool) (Decision, uint32) {
	owner, ownNumber := s.matchOwnNumber(n, explain)
	base, baseNumber := s.base.matchSelectedNumber(n, explain, s.selection)
	winner, number := owner, ownNumber
	if baseNumber != 0 {
		better := ownNumber == 0
		if !better {
			a, b := s.rules[ownNumber-1], s.base.rules[baseNumber-1]
			as, bs := scopeRank(s.scope(a).Kind), scopeRank(s.base.scope(b).Kind)
			better = bs > as || bs == as && (b.class < a.class || b.class == a.class && (b.score > a.score || b.score == a.score && base.RuleID < owner.RuleID))
		}
		if better {
			winner, number = base, baseNumber+uint32(len(s.rules))
		}
	}
	winner.Generation = s.generation
	if explain {
		winner.SourceIDs = append(owner.SourceIDs, base.SourceIDs...)
		slices.Sort(winner.SourceIDs)
		winner.SourceIDs = slices.Compact(winner.SourceIDs)
	}
	return winner, number
}
