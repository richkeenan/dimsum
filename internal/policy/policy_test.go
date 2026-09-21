package policy_test

import (
	"strings"
	"testing"

	"github.com/richkeenan/dimsum/internal/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func name(t *testing.T, s string) policy.Name {
	t.Helper()
	n, err := policy.NormalizeName(s)
	require.NoError(t, err)
	return n
}

func compile(t *testing.T, rules ...policy.Rule) *policy.Matcher {
	t.Helper()
	m, err := policy.Compile(7, rules, policy.DefaultLimits())
	require.NoError(t, err)
	return m
}

func rule(id string, kind policy.Kind, class policy.Class, pattern string) policy.Rule {
	return policy.Rule{ID: id, SourceID: "source-" + id, SourceText: pattern, Kind: kind, Class: class, Pattern: pattern}
}

// Literal cases detect lost label boundaries, wildcard apex leakage,
// glob crossing labels, and accidental anchoring of substring regexes.
func TestRuleForms(t *testing.T) {
	for _, tc := range []struct {
		kind    policy.Kind
		pattern string
		yes, no []string
	}{
		{policy.Exact, "Example.COM.", []string{"example.com", "EXAMPLE.COM."}, []string{"x.example.com", "badexample.com"}},
		{policy.Suffix, "example.com", []string{"example.com", "a.b.example.com"}, []string{"badexample.com", "example.com.evil"}},
		{policy.Wildcard, "*.example.com", []string{"x.example.com", "a.b.example.com"}, []string{"example.com", "badexample.com"}},
		{policy.Glob, "ads?.example.com", []string{"ads1.example.com"}, []string{"ads.example.com", "ads12.example.com", "ads.a.example.com"}},
		{policy.Glob, "ad*s.example.com", []string{"ads.example.com", "adverts.example.com"}, []string{"ad.x.s.example.com"}},
		{policy.Glob, "*.example.com", []string{"x.example.com", "a.b.example.com"}, []string{"example.com"}},
		{policy.Regex, "ads", []string{"ads.example.com", "myads.test"}, []string{"example.com"}},
	} {
		t.Run(tc.pattern+string(tc.kind), func(t *testing.T) {
			m := compile(t, rule("deny", tc.kind, policy.CustomDeny, tc.pattern))
			for _, s := range tc.yes {
				assert.Equal(t, policy.Block, m.Match(name(t, s)).Result, s)
			}
			for _, s := range tc.no {
				assert.Equal(t, policy.Forward, m.Match(name(t, s)).Result, s)
			}
		})
	}
}

func TestAllowPrecedenceAndAmplitude(t *testing.T) {
	for _, kind := range []policy.Kind{policy.Exact, policy.Suffix, policy.Wildcard, policy.Glob, policy.Regex} {
		patterns := map[policy.Kind]string{policy.Exact: "a.amplitude.com", policy.Suffix: "amplitude.com", policy.Wildcard: "*.amplitude.com", policy.Glob: "?.amplitude.com", policy.Regex: `(^|\.)amplitude\.com$`}
		m := compile(t, rule("deny", policy.Exact, policy.CustomDeny, "a.amplitude.com"), rule("allow", kind, policy.CustomAllow, patterns[kind]))
		assert.Equal(t, "allow", m.Match(name(t, "a.amplitude.com")).RuleID)
	}
	m := compile(t, rule("allow", policy.Regex, policy.CustomAllow, `(^|\.)amplitude\.com$`), rule("deny", policy.Regex, policy.CustomDeny, `.`))
	for _, s := range []string{"amplitude.com", "api.amplitude.com", "a.b.amplitude.com", "AMPLITUDE.COM."} {
		assert.Equal(t, policy.Allow, m.Match(name(t, s)).Result, s)
	}
	for _, s := range []string{"notamplitude.com", "amplitude.com.evil", "amplitudeXcom"} {
		assert.Equal(t, policy.Block, m.Match(name(t, s)).Result, s)
	}
}

func TestClassPrecedence(t *testing.T) {
	classes := []policy.Class{policy.CustomAllow, policy.CustomDeny, policy.SubscriptionAllow, policy.SubscriptionDeny, policy.SpecialDeny}
	for i, higher := range classes {
		for _, lower := range classes[i+1:] {
			m := compile(t, rule("lower", policy.Exact, lower, "x.test"), rule("higher", policy.Suffix, higher, "test"))
			assert.Equal(t, "higher", m.Match(name(t, "x.test")).RuleID)
		}
	}
}

func TestSpecificityAndExplanation(t *testing.T) {
	rules := []policy.Rule{
		rule("a-regex", policy.Regex, policy.CustomDeny, `test$`),
		rule("b-broad", policy.Suffix, policy.CustomDeny, "test"),
		rule("c-narrow", policy.Suffix, policy.CustomDeny, "x.test"),
		rule("e-exact", policy.Exact, policy.CustomDeny, "x.test"),
		rule("d-exact", policy.Exact, policy.CustomDeny, "x.test"),
	}
	for range 2 {
		m := compile(t, rules...)
		d := m.Evaluate(policy.Query{Original: name(t, "x.test"), Name: name(t, "x.test"), Explain: true})
		assert.Equal(t, "d-exact", d.RuleID)
		assert.Equal(t, []string{"source-a-regex", "source-b-broad", "source-c-narrow", "source-d-exact", "source-e-exact"}, d.SourceIDs)
		assert.Nil(t, m.Match(name(t, "x.test")).SourceIDs)
		for i, j := 0, len(rules)-1; i < j; i, j = i+1, j-1 {
			rules[i], rules[j] = rules[j], rules[i]
		}
	}
	m := compile(t, rules[:3]...)
	assert.Equal(t, "c-narrow", m.Match(name(t, "x.test")).RuleID)
}

func TestOriginalAllowAndTargetScope(t *testing.T) {
	m := compile(t,
		rule("original", policy.Exact, policy.CustomAllow, "site.test"),
		rule("target", policy.Exact, policy.SubscriptionAllow, "good.alias.test"),
		rule("deny", policy.Suffix, policy.CustomDeny, "bad.test"),
		rule("feed", policy.Suffix, policy.SubscriptionDeny, "alias.test"))
	assert.Equal(t, policy.Allow, m.Evaluate(policy.Query{Original: name(t, "site.test"), Name: name(t, "bad.test")}).Result)
	assert.Equal(t, policy.Allow, m.Evaluate(policy.Query{Original: name(t, "other.test"), Name: name(t, "good.alias.test")}).Result)
	assert.Equal(t, policy.Block, m.Evaluate(policy.Query{Original: name(t, "other.test"), Name: name(t, "bad.alias.test")}).Result)
	assert.Equal(t, policy.Allow, m.Evaluate(policy.Query{Original: name(t, "good.alias.test"), Name: name(t, "bad.test")}).Result)
	assert.Equal(t, policy.Local, m.Evaluate(policy.Query{Original: name(t, "site.test"), Name: name(t, "bad.test"), Local: true, Paused: true}).Result)
	assert.Equal(t, policy.Paused, m.Evaluate(policy.Query{Original: name(t, "bad.test"), Name: name(t, "bad.test"), Paused: true}).Result)
}

func TestGenerationIsolation(t *testing.T) {
	rules := []policy.Rule{rule("old", policy.Exact, policy.CustomDeny, "old.test")}
	old := compile(t, rules...)
	rules[0].Pattern = "new.test"
	rules[0].ID = "new"
	newer, err := policy.Compile(8, rules, policy.DefaultLimits())
	require.NoError(t, err)
	assert.Equal(t, policy.Block, old.Match(name(t, "old.test")).Result)
	assert.Equal(t, policy.Forward, newer.Match(name(t, "old.test")).Result)
	assert.Equal(t, uint64(7), old.Match(name(t, "old.test")).Generation)
	assert.Equal(t, uint64(8), newer.Match(name(t, "new.test")).Generation)
	rules[0].Kind = "unknown"
	failed, err := policy.Compile(9, rules, policy.DefaultLimits())
	assert.Error(t, err)
	assert.Nil(t, failed)
	assert.Equal(t, "old", old.Match(name(t, "old.test")).RuleID)
}

func TestConfigurationNormalization(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"BÜCHER.Example.", "xn--bcher-kva.example"}, {"faß.de", "xn--fa-hia.de"}, {"EXAMPLE。COM。", "example.com"},
	} {
		n, err := policy.NormalizeName(tc.input)
		require.NoError(t, err)
		assert.Equal(t, tc.want, n.Display())
		m := compile(t, rule("idn", policy.Exact, policy.CustomDeny, tc.input))
		assert.Equal(t, policy.Block, m.Match(name(t, tc.want)).Result)
	}
	for _, s := range []string{"", ".", "example..", "a..test", "a b.test", "-a.test", "a-.test", "xn--.test", strings.Repeat("a", 64) + ".test", strings.Repeat("a.", 128)} {
		_, err := policy.NormalizeName(s)
		assert.Error(t, err, s)
	}
	for _, tc := range []struct {
		kind    policy.Kind
		pattern string
	}{
		{policy.Glob, "a[bc].test"}, {policy.Glob, `a\*.test`}, {policy.Glob, "a**.test"}, {policy.Glob, "bü?.test"}, {policy.Wildcard, "example.com"}, {policy.Exact, "*.test"},
	} {
		_, err := policy.Compile(1, []policy.Rule{rule("bad", tc.kind, policy.CustomDeny, tc.pattern)}, policy.DefaultLimits())
		assert.Error(t, err, tc.pattern)
	}
}

func TestWireNamesAreBinarySafe(t *testing.T) {
	wire := []byte{5, 'A', '.', 0, 255, '\\', 4, 'T', 'E', 'S', 'T', 0}
	n, err := policy.NameFromWire(wire)
	require.NoError(t, err)
	assert.Equal(t, `a\046\000\255\092.test`, n.Display())
	wire[1] = 'z'
	assert.Equal(t, `a\046\000\255\092.test`, n.Display())
	m := compile(t, rule("suffix", policy.Suffix, policy.CustomDeny, "test"))
	assert.Equal(t, policy.Block, m.Match(n).Result)
	// Embedded length-prefixed bytes are not actual label boundaries.
	fake, err := policy.NameFromWire([]byte{6, 'x', 4, 't', 'e', 's', 't', 0})
	require.NoError(t, err)
	assert.Equal(t, policy.Forward, m.Match(fake).Result)
	for _, b := range [][]byte{nil, {1, 'a'}, {0, 0}, {0xc0, 0}, {64}, {2, 'a', 0}} {
		_, err := policy.NameFromWire(b)
		assert.Error(t, err, b)
	}
	root, err := policy.NameFromWire([]byte{0})
	require.NoError(t, err)
	assert.Equal(t, "", root.Display())
}

func TestRuleIdentityValidation(t *testing.T) {
	r := rule("id", policy.Exact, policy.CustomDeny, "test")
	_, err := policy.Compile(1, []policy.Rule{r, r}, policy.DefaultLimits())
	assert.Error(t, err)
	r.ID = ""
	_, err = policy.Compile(1, []policy.Rule{r}, policy.DefaultLimits())
	assert.Error(t, err)
	r.ID, r.Class = "id", "profile-allow"
	_, err = policy.Compile(1, []policy.Rule{r}, policy.DefaultLimits())
	assert.Error(t, err)
}

func TestRetainedDiagnosticText(t *testing.T) {
	r := rule("id", policy.Exact, policy.CustomDeny, "BÜCHER.Example.")
	r.SourceText = "  original configuration line with comment  "
	m := compile(t, r)
	got, ok := m.Rule("id")
	require.True(t, ok)
	assert.Equal(t, r, got)
	got.Pattern = "mutated.test"
	assert.Equal(t, policy.Block, m.Match(name(t, "xn--bcher-kva.example")).Result)
	_, ok = m.Rule("missing")
	assert.False(t, ok)
}

func TestShadowedOriginalExceptionDoesNotExemptAliases(t *testing.T) {
	m := compile(t,
		rule("allow", policy.Exact, policy.SubscriptionAllow, "site.test"),
		rule("deny", policy.Exact, policy.CustomDeny, "site.test"),
		rule("alias", policy.Exact, policy.CustomDeny, "alias.test"))
	d := m.Evaluate(policy.Query{Original: name(t, "site.test"), Name: name(t, "alias.test"), Explain: true})
	assert.Equal(t, policy.Block, d.Result)
	assert.Equal(t, "alias", d.RuleID)
}

func TestIDNGlobAndBinaryOctetWildcard(t *testing.T) {
	m := compile(t, rule("glob", policy.Glob, policy.CustomDeny, "?.BÜCHER.Example."))
	assert.Equal(t, policy.Block, m.Match(name(t, "a.xn--bcher-kva.example")).Result)
	m = compile(t, rule("glob", policy.Glob, policy.CustomDeny, "?.test"))
	n, err := policy.NameFromWire([]byte{1, 255, 4, 't', 'e', 's', 't', 0})
	require.NoError(t, err)
	assert.Equal(t, policy.Block, m.Match(n).Result)
}

func TestGlobIDNASeparators(t *testing.T) {
	for _, separator := range []string{"。", "．", "｡"} {
		t.Run(separator, func(t *testing.T) {
			for _, pattern := range []string{
				"ads?.example" + separator + "com",
				"ads?" + separator + "example.com",
				"ads?.example.com" + separator,
			} {
				t.Run("valid/"+pattern, func(t *testing.T) {
					m := compile(t, rule("glob", policy.Glob, policy.CustomDeny, pattern))
					assert.Equal(t, policy.Block, m.Match(name(t, "ads1.example.com")).Result)
					assert.Equal(t, policy.Forward, m.Match(name(t, "ads12.example.com")).Result)
				})
			}
			t.Run("descendants", func(t *testing.T) {
				m := compile(t, rule("glob", policy.Glob, policy.CustomDeny, "*"+separator+"example.com"))
				assert.Equal(t, policy.Block, m.Match(name(t, "a.b.example.com")).Result)
				assert.Equal(t, policy.Forward, m.Match(name(t, "example.com")).Result)
			})
			for _, pattern := range []string{
				"ads?.example" + separator + ".com",
				"ads?.example." + separator + "com",
				"ads?.example" + separator + separator + "com",
				"ads?.example.com" + separator + ".",
				"ads?.example.com." + separator,
				"ads?.example.com" + separator + separator,
			} {
				t.Run("invalid/"+pattern, func(t *testing.T) {
					m, err := policy.Compile(1, []policy.Rule{rule("glob", policy.Glob, policy.CustomDeny, pattern)}, policy.DefaultLimits())
					assert.Error(t, err)
					assert.Nil(t, m)
				})
			}
		})
	}
}
