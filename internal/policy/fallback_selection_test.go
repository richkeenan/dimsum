package policy

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Ineligible fallback rules must not require formatting a name or running a
// pattern. Adding another device's rules must not add allocations to this one.
func TestExcludedFallbackAllocationBudget(t *testing.T) {
	for _, kind := range []Kind{Regex, Glob, Wildcard} {
		t.Run(string(kind), func(t *testing.T) {
			pattern := `^ads[.]example[.]test$`
			if kind == Glob {
				pattern = "a?s.example.test"
			} else if kind == Wildcard {
				pattern = "*.example.test"
			}
			base, err := CompileSnapshot(1, []Rule{{ID: "feed", SourceID: "excluded", Kind: kind, Class: SubscriptionDeny, Pattern: pattern}}, DefaultLimits())
			require.NoError(t, err)
			root, err := CompileOverlay(2, []Rule{{ID: "scoped-custom:other", Scope: Scope{Kind: ClientScope, ID: "other"}, SourceID: "custom", Kind: kind, Class: CustomDeny, Pattern: pattern}}, base, DefaultLimits())
			require.NoError(t, err)
			selected := root.WithSelection(NewSelection("", "device", nil))
			name, err := NormalizeName("ads.example.test")
			require.NoError(t, err)
			var decision Decision
			allocations := testing.AllocsPerRun(100, func() { decision = selected.Match(name) })
			assert.Equal(t, Forward, decision.Result)
			assert.Zero(t, allocations)
			// Selection must not damage other views or change diagnostic numbering.
			allowed := root.WithSelection(NewSelection("", "other", []string{"excluded"}))
			d, number := allowed.EvaluateNumber(Query{Original: name, Name: name, Explain: true})
			assert.Equal(t, Block, d.Result)
			assert.Equal(t, "scoped-custom:other", d.RuleID)
			rule, ok := root.RuleAt(number)
			require.True(t, ok)
			assert.Equal(t, d.RuleID, rule.ID)
			assert.ElementsMatch(t, []string{"custom", "excluded"}, d.SourceIDs)
			assert.Same(t, base, selected.base, "subscription arenas and snapshot must remain shared")
		})
	}
}

func BenchmarkExcludedFallback(b *testing.B) {
	for _, count := range []int{0, 1, 64} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			var rules []Rule
			for i := 0; i < count; i++ {
				rules = append(rules, Rule{ID: fmt.Sprintf("scoped-custom:other:%d", i), Scope: Scope{Kind: ClientScope, ID: "other"}, SourceID: "custom", Kind: Regex, Class: CustomDeny, Pattern: fmt.Sprintf("ads%d[.]example[.]test$", i)})
			}
			root, err := CompileSnapshot(1, rules, DefaultLimits())
			require.NoError(b, err)
			selected := root.WithSelection(NewSelection("", "device", nil))
			name, err := NormalizeName("www.example.test")
			require.NoError(b, err)
			var decision Decision
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				decision, _ = selected.EvaluateNumber(Query{Original: name, Name: name})
			}
			b.StopTimer()
			assert.Equal(b, Forward, decision.Result)
		})
	}
}
