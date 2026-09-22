package policy

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Comparing every pair of classes and forms catches choosing a layer before
// applying precedence, dropping duplicate provenance, and incorrect rule offsets.
func TestOverlayDifferential(t *testing.T) {
	forms := []struct {
		kind    Kind
		pattern string
	}{
		{Exact, "ads.example"}, {Suffix, "example"}, {Wildcard, "*.example"},
		{Glob, "a?s.example"}, {Regex, `^ads\.example$`},
	}
	for _, ownerClass := range []Class{CustomAllow, CustomDeny, SpecialDeny} {
		for _, subClass := range []Class{SubscriptionAllow, SubscriptionDeny} {
			for _, ownerForm := range forms {
				for _, subForm := range forms {
					t.Run(fmt.Sprintf("%s/%s/%s/%s", ownerClass, subClass, ownerForm.kind, subForm.kind), func(t *testing.T) {
						subs := []Rule{
							{ID: "feed:z", SourceID: "feed-z", SourceText: "original feed", Class: subClass, Kind: subForm.kind, Pattern: subForm.pattern},
							{ID: "feed:a", SourceID: "feed-a", Class: subClass, Kind: subForm.kind, Pattern: subForm.pattern},
						}
						prefix := "custom:"
						if ownerClass == SpecialDeny {
							prefix = "special:"
						}
						owners := []Rule{
							{ID: prefix + "z", SourceID: "owner", Class: ownerClass, Kind: ownerForm.kind, Pattern: ownerForm.pattern},
							{ID: prefix + "a", SourceID: "owner", Class: ownerClass, Kind: ownerForm.kind, Pattern: ownerForm.pattern},
						}
						base, err := CompileSnapshot(1, subs, DefaultLimits())
						require.NoError(t, err)
						for _, input := range [][]Rule{nil, owners, owners[:1], nil} {
							layered, err := CompileOverlay(2, input, base, DefaultLimits())
							require.NoError(t, err)
							flat, err := CompileSnapshot(2, append(append([]Rule(nil), input...), subs...), DefaultLimits())
							require.NoError(t, err)
							for _, text := range []string{"ads.example", "example", "x.ads.example", "absent.test"} {
								n, err := NormalizeName(text)
								require.NoError(t, err)
								assert.Equal(t, flat.Match(n), layered.Match(n))
								for _, q := range []Query{{Original: n, Name: n}, {Original: n, Name: n, Explain: true}, {Original: n, Name: n, Local: true, Explain: true}, {Original: n, Name: n, Paused: true, Explain: true}} {
									assert.Equal(t, flat.Evaluate(q), layered.Evaluate(q))
									d, number := layered.EvaluateNumber(q)
									_, flatNumber := flat.EvaluateNumber(q)
									assert.Equal(t, flatNumber, number)
									if number != 0 {
										r, ok := layered.RuleAt(number)
										require.True(t, ok)
										assert.Equal(t, d.RuleID, r.ID)
									}
								}
								assert.Equal(t, uint64(1), base.Match(n).Generation)
							}
							for i, r := range append(append([]Rule(nil), input...), subs...) {
								got, ok := layered.Rule(r.ID)
								require.True(t, ok)
								assert.Equal(t, r, got)
								got, ok = layered.RuleAt(uint32(i + 1))
								require.True(t, ok)
								assert.Equal(t, r, got)
							}
							_, ok := layered.RuleAt(0)
							assert.False(t, ok)
							_, ok = layered.RuleAt(uint32(len(input) + len(subs) + 1))
							assert.False(t, ok)
							_, ok = layered.Rule("missing")
							assert.False(t, ok)
						}
					})
				}
			}
		}
	}
}

func TestOverlayIsolationAndOriginalAllow(t *testing.T) {
	subs := []Rule{{ID: "feed:block", Class: SubscriptionDeny, Kind: Exact, Pattern: "ads.example", SourceID: "feed"}}
	base, err := CompileSnapshot(1, subs, DefaultLimits())
	require.NoError(t, err)
	owners := []Rule{{ID: "custom:allow", Class: CustomAllow, Kind: Exact, Pattern: "original.example", SourceID: "owner"}}
	old, err := CompileOverlay(2, owners, base, DefaultLimits())
	require.NoError(t, err)
	next, err := CompileOverlay(3, nil, base, DefaultLimits())
	require.NoError(t, err)
	original, err := NormalizeName("original.example")
	require.NoError(t, err)
	alias, err := NormalizeName("ads.example")
	require.NoError(t, err)
	q := Query{Original: original, Name: alias, Explain: true}
	assert.Equal(t, Decision{Result: Allow, Generation: 2, RuleID: "custom:allow", SourceIDs: []string{"feed", "owner"}}, old.Evaluate(q))
	assert.Equal(t, Block, next.Evaluate(q).Result)
	assert.Equal(t, Block, old.Evaluate(Query{Original: alias, Name: alias}).Result, "an alias allow must not escape its original question")
	owners[0].Pattern = "changed.example"
	subs[0].Pattern = "changed.example"
	r, ok := old.Rule("feed:block")
	require.True(t, ok)
	r.Pattern = "changed.example"
	assert.Equal(t, Allow, old.Evaluate(q).Result)
	assert.Equal(t, Block, base.Match(alias).Result)
	assert.Equal(t, Block, next.Match(alias).Result)
	assert.Equal(t, uint64(1), base.Match(alias).Generation)
	allocs := testing.AllocsPerRun(1000, func() { next.EvaluateNumber(Query{Original: alias, Name: alias}) })
	assert.Zero(t, allocs)
	allocs = testing.AllocsPerRun(1000, func() { old.EvaluateNumber(Query{Original: original, Name: original}) })
	assert.Zero(t, allocs)
	allocs = testing.AllocsPerRun(1000, func() { old.EvaluateNumber(Query{Original: alias, Name: alias}) })
	assert.Zero(t, allocs)
}

func TestOverlayRejectsInvalidComponents(t *testing.T) {
	valid := Rule{ID: "feed:a", Class: SubscriptionDeny, Kind: Exact, Pattern: "example"}
	for _, r := range []Rule{
		{ID: "custom:a", Class: SubscriptionDeny, Kind: Exact, Pattern: "example"},
		{ID: "special:a", Class: SubscriptionAllow, Kind: Exact, Pattern: "example"},
		{ID: "feed:a", Class: CustomDeny, Kind: Exact, Pattern: "example"},
		{ID: "feed:a", Class: SpecialDeny, Kind: Exact, Pattern: "example"},
	} {
		base, err := CompileSnapshot(1, []Rule{r}, DefaultLimits())
		require.NoError(t, err, "flat compilation remains permissive")
		_, err = CompileOverlay(2, nil, base, DefaultLimits())
		require.Error(t, err)
	}
	base, err := CompileSnapshot(1, []Rule{valid}, DefaultLimits())
	require.NoError(t, err)
	for _, r := range []Rule{
		valid,
		{ID: "feed:a", Class: CustomDeny, Kind: Exact, Pattern: "example"},
		{ID: "custom:a", Class: SubscriptionAllow, Kind: Exact, Pattern: "example"},
		{ID: "custom:a", Class: SpecialDeny, Kind: Exact, Pattern: "example"},
		{ID: "special:a", Class: CustomAllow, Kind: Exact, Pattern: "example"},
	} {
		_, err := CompileOverlay(2, []Rule{r}, base, DefaultLimits())
		require.Error(t, err)
	}
	layered, err := CompileOverlay(2, nil, base, DefaultLimits())
	require.NoError(t, err)
	_, err = CompileOverlay(3, nil, layered, DefaultLimits())
	require.Error(t, err)
	_, err = CompileOverlay(3, nil, nil, DefaultLimits())
	require.Error(t, err)
}

func TestOverlayAggregateRegexBudgets(t *testing.T) {
	sub := Rule{ID: "feed:a", Class: SubscriptionDeny, Kind: Regex, Pattern: `ads\.example`}
	owner := Rule{ID: "custom:a", Class: CustomDeny, Kind: Regex, Pattern: sub.Pattern}
	base, err := CompileSnapshot(1, []Rule{sub}, DefaultLimits())
	require.NoError(t, err)
	limits := DefaultLimits()
	limits.MaxRegex = 1
	_, err = CompileOverlay(2, []Rule{owner}, base, limits)
	require.Error(t, err)
	limits = DefaultLimits()
	limits.MaxTotalRegexBytes = 10000
	_, err = CompileSnapshot(1, []Rule{sub}, limits)
	require.NoError(t, err)
	_, err = CompileOverlay(2, []Rule{owner}, base, limits)
	require.Error(t, err)
	limits = DefaultLimits()
	limits.MaxExpressionBytes = 2
	_, err = CompileOverlay(2, nil, base, limits)
	require.Error(t, err)
	limits = DefaultLimits()
	limits.MaxProgramInstructions = 2
	_, err = CompileOverlay(2, nil, base, limits)
	require.Error(t, err)
	limits = DefaultLimits()
	limits.MaxRegex = 0
	_, err = CompileOverlay(2, nil, base, limits)
	require.Error(t, err)
}

func TestOverlayAggregateSnapshotBudgets(t *testing.T) {
	sub := Rule{ID: "feed:a", Class: SubscriptionDeny, Kind: Exact, Pattern: "example", SourceText: strings.Repeat("x", 1000)}
	base, err := CompileSnapshot(1, []Rule{sub}, DefaultLimits())
	require.NoError(t, err)
	owner := Rule{ID: "custom:a", Class: CustomDeny, Kind: Exact, Pattern: "example"}
	opts := DefaultSnapshotOptions()
	opts.MaxRules = 1
	_, err = compileOverlay(2, []Rule{owner}, base, DefaultLimits(), opts)
	require.Error(t, err)
	opts = DefaultSnapshotOptions()
	opts.MaxBytes = base.Memory().TotalBytes
	_, err = compileOverlay(2, nil, base, DefaultLimits(), opts)
	require.Error(t, err, "root metadata is retained too")
	// Interned source text makes input admission larger than retained storage.
	sub.SourceText = sub.Pattern
	sub.SourceID = strings.Repeat("s", 2000)
	subs := []Rule{sub, sub}
	subs[1].ID = "feed:b"
	base, err = CompileSnapshot(1, subs, DefaultLimits())
	require.NoError(t, err)
	opts = DefaultSnapshotOptions()
	opts.MaxBytes = 4000
	_, err = compileOverlay(2, nil, base, DefaultLimits(), opts)
	require.Error(t, err, "aggregate input admission survives interning")
}

func TestOverlayMixedSpecificity(t *testing.T) {
	rng := rand.New(rand.NewSource(23))
	var owners, subs []Rule
	for i := range 180 {
		class := snapshotClasses[rng.Intn(len(snapshotClasses))]
		kind := snapshotKinds[rng.Intn(len(snapshotKinds))]
		pattern := fmt.Sprintf("n%d.zone%d.example", rng.Intn(10), rng.Intn(3))
		switch kind {
		case Wildcard:
			pattern = "*." + pattern
		case Glob:
			pattern = "n?.zone?.example"
		case Regex:
			pattern = `(^|\.)n[12]\.zone[01]\.example$`
		}
		prefix := "feed:"
		if class == CustomAllow || class == CustomDeny {
			prefix = "custom:"
		}
		if class == SpecialDeny {
			prefix = "special:"
		}
		r := Rule{ID: fmt.Sprintf("%s%03d", prefix, i), SourceID: fmt.Sprintf("source%d", i%7), Class: class, Kind: kind, Pattern: pattern}
		if prefix == "feed:" {
			subs = append(subs, r)
		} else {
			owners = append(owners, r)
		}
	}
	base, err := CompileSnapshot(1, subs, DefaultLimits())
	require.NoError(t, err)
	for _, input := range [][]Rule{nil, owners, owners[:len(owners)/2]} {
		layered, err := CompileOverlay(2, input, base, DefaultLimits())
		require.NoError(t, err)
		flat, err := CompileSnapshot(2, append(append([]Rule(nil), input...), subs...), DefaultLimits())
		require.NoError(t, err)
		for i := range 120 {
			n, err := NormalizeName(fmt.Sprintf("n%d.zone%d.example", i%12, i%4))
			require.NoError(t, err)
			original, err := NormalizeName(fmt.Sprintf("n%d.zone%d.example", (i+3)%12, (i+1)%4))
			require.NoError(t, err)
			q := Query{Original: original, Name: n, Explain: true}
			assert.Equal(t, flat.Evaluate(q), layered.Evaluate(q))
			q.Name, err = NormalizeName("child." + n.Display())
			require.NoError(t, err)
			assert.Equal(t, flat.Evaluate(q), layered.Evaluate(q))
		}
	}
}

func TestOverlayMemoryAndRegexBoundary(t *testing.T) {
	subs := []Rule{{ID: "feed:a", Class: SubscriptionDeny, Kind: Regex, Pattern: `(a|b){2,3}`}}
	owners := []Rule{{ID: "custom:a", Class: CustomAllow, Kind: Regex, Pattern: `^ads\.example$`}}
	base, err := CompileSnapshot(1, subs, DefaultLimits())
	require.NoError(t, err)
	owner, err := CompileSnapshot(2, owners, DefaultLimits())
	require.NoError(t, err)
	limits := DefaultLimits()
	limits.MaxRegex = 2
	limits.MaxTotalRegexBytes = base.resources.regexBytes + owner.resources.regexBytes
	limits.MaxExpressionBytes = max(base.resources.maxExpressionBytes, owner.resources.maxExpressionBytes)
	limits.MaxProgramInstructions = max(base.resources.maxProgramInstructions, owner.resources.maxProgramInstructions)
	flat, err := CompileSnapshot(2, append(append([]Rule(nil), owners...), subs...), limits)
	require.NoError(t, err)
	layered, err := CompileOverlay(2, owners, base, limits)
	require.NoError(t, err)
	assert.Equal(t, 2, layered.Memory().Rules)
	assert.Equal(t, base.Memory().TotalBytes+owner.Memory().TotalBytes, layered.Memory().TotalBytes)
	assert.Equal(t, flat.resources.regexBytes, layered.resources.regexBytes)
	assert.Same(t, base, layered.base, "composition must retain the unchanged subscription object")
	// Disabled invalid component inputs do not contaminate compiled summaries.
	opts := DefaultSnapshotOptions()
	opts.DisabledSources = map[string]bool{"disabled": true}
	base, err = CompileSnapshotWithOptions(1, []Rule{{ID: "custom:disabled", Class: CustomDeny, Kind: Regex, Pattern: "[", SourceID: "disabled"}}, limits, opts)
	require.NoError(t, err)
	_, err = CompileOverlay(2, nil, base, limits)
	require.NoError(t, err)
}

func BenchmarkOverlayCompile(b *testing.B) {
	for _, count := range []int{1, 1000, 76000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			rules := make([]Rule, count)
			for i := range rules {
				rules[i] = Rule{ID: fmt.Sprintf("feed:%d", i), Class: SubscriptionDeny, Kind: Exact, Pattern: fmt.Sprintf("n%d.example", i)}
			}
			base, err := CompileSnapshot(1, rules, DefaultLimits())
			if err != nil {
				b.Fatal(err)
			}
			owners := []Rule{{ID: "custom:a", Class: CustomAllow, Kind: Exact, Pattern: "n0.example"}}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if _, err := CompileOverlay(2, owners, base, DefaultLimits()); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
