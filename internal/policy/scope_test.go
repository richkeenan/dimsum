package policy

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestScopedEligibilityAndPrecedence(t *testing.T) {
	for _, kind := range []Kind{Exact, Suffix, Glob, Regex} {
		t.Run(string(kind), func(t *testing.T) {
			pattern := "ads.example"
			if kind == Glob {
				pattern = "a?s.example"
			}
			if kind == Regex {
				pattern = `^ads\.example$`
			}
			rules := []Rule{
				{ID: "custom:network", SourceID: "custom", Kind: kind, Class: CustomAllow, Pattern: pattern},
				{ID: "custom:profile", SourceID: "custom", Scope: Scope{Kind: ProfileScope, ID: "kids"}, Kind: kind, Class: CustomDeny, Pattern: pattern},
				{ID: "custom:device", SourceID: "custom", Scope: Scope{Kind: ClientScope, ID: "tablet"}, Kind: kind, Class: CustomAllow, Pattern: pattern},
			}
			s, err := CompileSnapshot(9, rules, DefaultLimits())
			require.NoError(t, err)
			n, err := NormalizeName("ads.example")
			require.NoError(t, err)
			assert.Equal(t, "custom:network", s.Match(n).RuleID)
			p := s.WithSelection(NewSelection("kids", "", nil))
			assert.Equal(t, Block, p.Match(n).Result)
			d, number := s.WithSelection(NewSelection("kids", "tablet", nil)).EvaluateNumber(Query{Original: n, Name: n, Explain: true})
			assert.Equal(t, "custom:device", d.RuleID)
			assert.Equal(t, Scope{Kind: ClientScope, ID: "tablet"}, d.Scope)
			r, ok := s.RuleAt(number)
			require.True(t, ok)
			assert.Equal(t, d.RuleID, r.ID)
		})
	}
}

func TestSourceMaskBeforeDuplicateWinnerAndAliasAllow(t *testing.T) {
	for _, kind := range []Kind{Exact, Suffix, Glob, Regex} {
		t.Run(string(kind), func(t *testing.T) {
			pattern := "ads.example"
			if kind == Regex {
				pattern = `^ads\.example$`
			}
			base, err := CompileSnapshot(1, []Rule{
				{ID: "a", SourceID: "off", Kind: kind, Class: SubscriptionAllow, Pattern: pattern},
				{ID: "b", SourceID: "on", Kind: kind, Class: SubscriptionDeny, Pattern: pattern},
			}, DefaultLimits())
			require.NoError(t, err)
			s, err := CompileOverlay(7, []Rule{{ID: "custom:original", Scope: Scope{Kind: ClientScope, ID: "tablet"}, Kind: Exact, Class: CustomAllow, Pattern: "original.example"}}, base, DefaultLimits())
			require.NoError(t, err)
			n, _ := NormalizeName("ads.example")
			original, _ := NormalizeName("original.example")
			selected := s.WithSelection(NewSelection("", "tablet", []string{"on"}))
			d := selected.Evaluate(Query{Original: n, Name: n, Explain: true})
			assert.Equal(t, Block, d.Result)
			assert.Equal(t, []string{"on"}, d.SourceIDs)
			assert.Equal(t, Allow, selected.Evaluate(Query{Original: original, Name: n}).Result)
			assert.Equal(t, Allow, s.Match(n).Result)
		})
	}
}

func BenchmarkScopedMatch(b *testing.B) {
	s, err := CompileSnapshot(1, []Rule{{ID: "a", SourceID: "on", Kind: Suffix, Class: SubscriptionDeny, Pattern: "example"}}, DefaultLimits())
	require.NoError(b, err)
	s = s.WithSelection(NewSelection("", "device", []string{"on"}))
	n, _ := NormalizeName("ads.example")
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		s.Match(n)
	}
}

func TestScopedLocalAndPauseHaveNoWinningRule(t *testing.T) {
	s, err := CompileSnapshot(1, []Rule{{ID: "custom:device", Scope: Scope{Kind: ClientScope, ID: "tablet"}, Kind: Exact, Class: CustomDeny, Pattern: "ads.example"}}, DefaultLimits())
	require.NoError(t, err)
	s = s.WithSelection(NewSelection("", "tablet", nil))
	n, _ := NormalizeName("ads.example")
	for _, q := range []Query{{Original: n, Name: n, Local: true}, {Original: n, Name: n, Paused: true}} {
		d, number := s.EvaluateNumber(q)
		assert.Empty(t, d.RuleID)
		assert.Zero(t, number)
		assert.Equal(t, Scope{}, d.Scope)
	}
}
