package policy_test

import (
	"strings"
	"testing"

	"github.com/richkeenan/dimsum/internal/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegexRejectsInvalidAndUnsupportedDialect(t *testing.T) {
	for _, expression := range []string{"[", `(?=ads)`, `(ads)\1`, `ads;querytype=A`} {
		r := rule("bad", policy.Regex, policy.CustomAllow, expression)
		if strings.Contains(expression, ";") {
			r.Dialect = "pihole"
		}
		m, err := policy.Compile(1, []policy.Rule{r}, policy.DefaultLimits())
		assert.Error(t, err, expression)
		assert.Nil(t, m)
	}
}

func TestRegexBudgetsRejectBeforePublication(t *testing.T) {
	for _, tc := range []struct {
		name     string
		limits   policy.Limits
		patterns []string
	}{
		{"count", policy.Limits{MaxRegex: 1, MaxExpressionBytes: 4096, MaxProgramInstructions: 8192, MaxTotalRegexBytes: 1 << 20}, []string{"one", "two"}},
		{"bytes", policy.Limits{MaxRegex: 10, MaxExpressionBytes: 3, MaxProgramInstructions: 8192, MaxTotalRegexBytes: 1 << 20}, []string{"four"}},
		{"program", policy.Limits{MaxRegex: 10, MaxExpressionBytes: 4096, MaxProgramInstructions: 32, MaxTotalRegexBytes: 1 << 20}, []string{`(?:ab){1000}`}},
		{"total", policy.Limits{MaxRegex: 10, MaxExpressionBytes: 4096, MaxProgramInstructions: 8192, MaxTotalRegexBytes: 1}, []string{"a"}},
		{"invalid limits", policy.Limits{}, []string{"a"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var rules []policy.Rule
			for _, p := range tc.patterns {
				rules = append(rules, rule(p, policy.Regex, policy.CustomAllow, p))
			}
			m, err := policy.Compile(1, rules, tc.limits)
			assert.Error(t, err)
			assert.Nil(t, m)
		})
	}
	_, err := policy.Compile(1, []policy.Rule{rule("huge", policy.Regex, policy.CustomDeny, strings.Repeat("a", 4097))}, policy.DefaultLimits())
	assert.Error(t, err)
}

func TestRegexUsesEscapedLowercasePresentation(t *testing.T) {
	n, err := policy.NameFromWire([]byte{3, 'A', '.', 0, 4, 'T', 'E', 'S', 'T', 0})
	require.NoError(t, err)
	m := compile(t, rule("binary", policy.Regex, policy.CustomDeny, `^a\\046\\000\.test$`))
	assert.Equal(t, policy.Block, m.Match(n).Result)
	assert.Equal(t, policy.Forward, m.Match(name(t, "a.test")).Result)
}

func TestRegexAggregateAndUnicodeProgramBudgets(t *testing.T) {
	limits := policy.DefaultLimits()
	limits.MaxTotalRegexBytes = 6000
	one := rule("one", policy.Regex, policy.CustomAllow, "one")
	two := rule("two", policy.Regex, policy.CustomAllow, "two")
	_, err := policy.Compile(1, []policy.Rule{one}, limits)
	require.NoError(t, err)
	_, err = policy.Compile(1, []policy.Rule{one, two}, limits)
	assert.Error(t, err, "budget must be cumulative across rules")
	// Large Unicode classes have many rune ranges despite few instructions.
	_, err = policy.Compile(1, []policy.Rule{rule("unicode", policy.Regex, policy.CustomAllow, `\p{L}`)}, limits)
	assert.Error(t, err, "rune tables must count against the memory budget")
}
