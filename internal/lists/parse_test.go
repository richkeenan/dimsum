package lists

import (
	"crypto/sha256"
	"fmt"
	"io"
	"math"
	"strings"
	"testing"

	"github.com/richkeenan/dimsum/internal/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type brokenReader struct{}

func TestInvalidDomainSkipped(t *testing.T) {
	for _, text := range []string{"123.א.example", "123.xn--4db.example", "123.a_b.א.example", "123_abc.xn--4db.example"} {
		r, err := Parse(strings.NewReader("valid.example\n"+text), Source{"s", Domains, policy.Suffix}, DefaultLimits())
		require.NoError(t, err)
		require.Len(t, r.Rules, 1)
		assert.Equal(t, "valid.example", r.Rules[0].Pattern)
		assert.Equal(t, 1, r.Rejected)
		require.Len(t, r.Diagnostics, 1)
		assert.Equal(t, 2, r.Diagnostics[0].Line)
		assert.Equal(t, "invalid-domain", r.Diagnostics[0].Code)
	}
}

func TestInvalidDomainDoesNotHideFatalSyntax(t *testing.T) {
	for _, aliases := range []string{"bad..example *.example", "*.example bad..example"} {
		r, err := Parse(strings.NewReader("0.0.0.0 valid.example\n0.0.0.0 "+aliases), Source{ID: "s", Dialect: Hosts}, DefaultLimits())
		require.Error(t, err)
		assert.Empty(t, r.Rules)
	}
	l := DefaultLimits()
	l.MaxDiagnostics = 1
	for _, tail := range []string{"||ads.example^$bad", "@@||bad..example^"} {
		r, err := Parse(strings.NewReader("||good.example^\n||bad..example^\n"+tail), Source{"s", Adblock, policy.Suffix}, l)
		require.Error(t, err)
		assert.Empty(t, r.Rules)
	}
	_, err := Parse(strings.NewReader("bad..example\n"), Source{"s", Domains, policy.Suffix}, l)
	require.Error(t, err)
}

func TestUnderscoreParserCompileInvariant(t *testing.T) {
	for _, tc := range []struct {
		d    Dialect
		text string
	}{
		{Hosts, "0.0.0.0 ads_with_underscore.example.test"},
		{Domains, "_sip._tcp.bücher.example"},
		{Adblock, "||ads_track.example^\n@@||safe_track.example^"},
	} {
		r, err := Parse(strings.NewReader(tc.text), Source{"s", tc.d, policy.Suffix}, DefaultLimits())
		require.NoError(t, err)
		require.NotEmpty(t, r.Rules)
		m, err := policy.Compile(1, r.Rules, policy.DefaultLimits())
		require.NoError(t, err)
		n, err := policy.NormalizeName(r.Rules[0].Pattern)
		require.NoError(t, err)
		assert.Equal(t, policy.Block, m.Match(n).Result)
	}
}

func (brokenReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestBoundariesAndReadErrors(t *testing.T) {
	s := Source{"s", Domains, policy.Exact}
	text := "a.example"
	for _, ending := range []string{"", "\n", "\r\n"} {
		input := text + ending
		l := Limits{int64(len(input)), len(text), 1, 1}
		r, err := Parse(strings.NewReader(input), s, l)
		require.NoError(t, err)
		require.Len(t, r.Rules, 1)
		assert.Equal(t, fmt.Sprintf("%x", sha256.Sum256([]byte(input))), r.SHA256)
		assert.Equal(t, text, r.Rules[0].SourceText)
	}
	r, err := Parse(io.MultiReader(strings.NewReader(text+"\n"), brokenReader{}), s, DefaultLimits())
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	assert.Empty(t, r.Rules)
	assert.Empty(t, r.SHA256)
	for _, n := range []int{math.MaxInt, math.MaxInt - 1} {
		l := DefaultLimits()
		l.MaxLineBytes = n
		require.NotPanics(t, func() { _, err = Parse(strings.NewReader(text), s, l) })
		require.Error(t, err)
	}
}

func TestDialects(t *testing.T) {
	for _, tt := range []struct {
		d        Dialect
		k        policy.Kind
		text     string
		patterns []string
	}{
		{Hosts, policy.Exact, "\ufeff# comment\r\n127.0.0.1 localhost\r\n0.0.0.0 ADS.Example alias.example # note\n::1 ip6-localhost\n", []string{"ads.example", "alias.example"}},
		{Domains, policy.Suffix, "# comment\nBÜCHER.example.\r\n", []string{"xn--bcher-kva.example"}},
		{Adblock, policy.Suffix, "[Adblock Plus 2.0]\n! comment\n||Ads.Example^\n@@||safe.example^\nplain.example\n", []string{"ads.example", "safe.example", "plain.example"}},
	} {
		t.Run(string(tt.d), func(t *testing.T) {
			r, err := Parse(strings.NewReader(tt.text), Source{"feed", tt.d, tt.k}, DefaultLimits())
			require.NoError(t, err)
			require.Len(t, r.Rules, len(tt.patterns))
			assert.NotEmpty(t, r.SHA256)
			for i, rule := range r.Rules {
				assert.Equal(t, tt.patterns[i], rule.Pattern)
				assert.Equal(t, "feed", rule.SourceID)
				assert.NotEmpty(t, rule.SourceText)
				assert.NotEmpty(t, rule.ID)
				assert.Equal(t, tt.k, rule.Kind)
			}
			if tt.d == Adblock {
				assert.Equal(t, policy.SubscriptionAllow, r.Rules[1].Class)
			}
		})
	}
}
func TestPublisherBoilerplate(t *testing.T) {
	for _, text := range []string{"255.255.255.255 broadcasthost", "fe80::1%lo0 localhost", "ff00::0 ip6-localnet", "ff00::0 ip6-mcastprefix", "ff02::3 ip6-allhosts", "0.0.0.0 0.0.0.0"} {
		r, err := Parse(strings.NewReader(text+"\n0.0.0.0 ads.example"), Source{ID: "s", Dialect: Hosts}, DefaultLimits())
		require.NoError(t, err)
		assert.Len(t, r.Rules, 1)
	}
	r, err := Parse(strings.NewReader("[Adblock Plus]\n||ads.example^"), Source{"s", Adblock, policy.Suffix}, DefaultLimits())
	require.NoError(t, err)
	assert.Len(t, r.Rules, 1)
}
func TestRejectWholeSource(t *testing.T) {
	for _, line := range []string{"||ads.example^$third-party", "@@||safe.example^$domain=site.example", "||ads.example/path^", "example.com##.ad", "/regex/", "https://example.com/", "@@example.com", "||*.example.com^", "[unknown]", "example.com extra", "1.2.3.4"} {
		t.Run(line, func(t *testing.T) {
			r, err := Parse(strings.NewReader("||valid.example^\n"+line), Source{"s", Adblock, policy.Suffix}, DefaultLimits())
			require.Error(t, err)
			assert.Empty(t, r.Rules)
			require.Len(t, r.Diagnostics, 1)
			assert.Equal(t, 2, r.Diagnostics[0].Line)
			assert.Equal(t, line, r.Diagnostics[0].Text)
		})
	}
	for _, text := range []string{"192.0.2.1 ads.example", "0.0.0.0 good.example bad/name"} {
		r, err := Parse(strings.NewReader(text), Source{ID: "s", Dialect: Hosts}, DefaultLimits())
		require.Error(t, err)
		assert.Empty(t, r.Rules)
	}
}
func TestLimitsAndEmpty(t *testing.T) {
	for _, l := range []Limits{{4, 100, 10, 2}, {100, 4, 10, 2}, {100, 100, 1, 2}, {}} {
		r, err := Parse(strings.NewReader("a.example\nb.example\n"), Source{"s", Domains, policy.Exact}, l)
		require.Error(t, err)
		assert.Empty(t, r.Rules)
	}
	for _, s := range []Source{{ID: "s", Dialect: Domains}, {ID: "s", Dialect: "unknown"}, {Dialect: Hosts}} {
		_, err := Parse(strings.NewReader("a.example"), s, DefaultLimits())
		require.Error(t, err)
	}
	_, err := Parse(strings.NewReader("# empty\n"), Source{ID: "s", Dialect: Hosts}, DefaultLimits())
	require.Error(t, err)
	l := DefaultLimits()
	l.MaxDiagnostics = 2
	r, err := Parse(strings.NewReader(strings.Repeat("bad/path\n", 10)), Source{"s", Domains, policy.Exact}, l)
	require.Error(t, err)
	assert.Len(t, r.Diagnostics, 2)
	assert.Equal(t, 10, r.Rejected)
}
func TestSourceMembership(t *testing.T) {
	var rules []policy.Rule
	for _, id := range []string{"one", "two"} {
		r, err := Parse(strings.NewReader("same.example\nsame.example\n"), Source{id, Domains, policy.Suffix}, DefaultLimits())
		require.NoError(t, err)
		require.Len(t, r.Rules, 2)
		rules = append(rules, r.Rules...)
	}
	m, err := policy.Compile(1, rules, policy.DefaultLimits())
	require.NoError(t, err)
	name, err := policy.NormalizeName("same.example")
	require.NoError(t, err)
	assert.Equal(t, []string{"one", "two"}, m.Evaluate(policy.Query{Name: name, Explain: true}).SourceIDs)
	m, err = policy.Compile(2, rules[2:], policy.DefaultLimits())
	require.NoError(t, err)
	assert.Equal(t, []string{"two"}, m.Evaluate(policy.Query{Name: name, Explain: true}).SourceIDs)
}
func FuzzParse(f *testing.F) {
	for _, s := range []string{"||example.com^", "@@||a.example^$important", "0.0.0.0 a.example b.example", "\ufeff# comment\r\n", "a.example", "0.0.0.0 ads_with_underscore.example.test", "_sip._tcp.bücher.example", "@@||safe_track.example^"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, text string) {
		for _, d := range []Dialect{Hosts, Domains, Adblock} {
			r, err := Parse(strings.NewReader(text), Source{"fuzz", d, policy.Suffix}, Limits{4096, 512, 64, 4})
			if err != nil {
				assert.Empty(t, r.Rules)
			} else {
				_, err = policy.Compile(1, r.Rules, policy.DefaultLimits())
				require.NoError(t, err)
			}
			assert.LessOrEqual(t, len(r.Diagnostics), 4)
		}
	})
}
