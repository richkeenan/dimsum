package policy

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerationScopedRuleNumber(t *testing.T) {
	rules := []Rule{{ID: "allow", Class: CustomAllow, Kind: Exact, Pattern: "allowed.example"}, {ID: "deny", Class: CustomDeny, Kind: Suffix, Pattern: "example"}}
	s, err := CompileSnapshot(42, rules, DefaultLimits())
	require.NoError(t, err)
	n, err := NormalizeName("blocked.example")
	require.NoError(t, err)
	d, number := s.EvaluateNumber(Query{Original: n, Name: n})
	assert.Equal(t, Block, d.Result)
	assert.EqualValues(t, 2, number)
	rule, ok := s.RuleAt(number)
	require.True(t, ok)
	assert.Equal(t, rules[1], rule)
	_, number = s.EvaluateNumber(Query{Original: n, Name: n, Paused: true})
	assert.Zero(t, number)
	_, ok = s.RuleAt(0)
	assert.False(t, ok)
	_, ok = s.RuleAt(3)
	assert.False(t, ok)
}

func TestCompactProvenanceRoundTrip(t *testing.T) {
	rules := []Rule{
		{ID: strings.Repeat("identifier", 8000), Class: CustomDeny, Kind: Exact, Pattern: "a.test", SourceID: "shared", SourceText: strings.Repeat("original source ", 5000)},
		{ID: "b", Class: SubscriptionDeny, Kind: Suffix, Pattern: "b.test", SourceID: "shared", SourceText: "b.test"},
		{ID: "c", Class: CustomAllow, Kind: Exact, Pattern: "c.test", SourceText: ""},
		{ID: "d", Class: CustomDeny, Kind: Regex, Pattern: "^d\\.test$", SourceID: "go", Dialect: "go", SourceText: "regex source"},
	}
	s, err := CompileSnapshot(1, rules, DefaultLimits())
	require.NoError(t, err)
	for i, want := range rules {
		got, ok := s.RuleAt(uint32(i + 1))
		require.True(t, ok)
		assert.Equal(t, want, got)
		got, ok = s.Rule(want.ID)
		require.True(t, ok)
		assert.Equal(t, want, got)
	}
	n, err := NormalizeName("child.b.test")
	require.NoError(t, err)
	assert.Equal(t, []string{"shared"}, s.Evaluate(Query{Original: n, Name: n, Explain: true}).SourceIDs)
}
