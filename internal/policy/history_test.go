package policy

import (
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
