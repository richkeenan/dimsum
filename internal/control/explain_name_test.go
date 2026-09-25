package control

import (
	"strings"
	"testing"

	"github.com/richkeenan/dimsum/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExplainAcceptsObservedDNSNames(t *testing.T) {
	s, store := dhcpFixture(t)
	_, err := s.Mutate(t.Context(), "rules", "POST", Mutation{Revision: store.Inspect().SavedRevision, Item: config.CustomRule{ID: "ads", Kind: "suffix", Action: "deny", Pattern: "ads.example", Enabled: true}})
	require.NoError(t, err)
	for _, tc := range []struct{ input, normalized string }{
		{"r1---edge.ads.example", "r1---edge.ads.example"},
		{`A\046B.ads.example`, `a\046b.ads.example`},
		{`\000\255.ads.example`, `\000\255.ads.example`},
		{"BÜCHER.ads.example.", "xn--bcher-kva.ads.example"},
		{" https://ADS.example/embed?source=popup#offers \n", "ads.example"},
		{"http://ads.example:8080/path", "ads.example"},
		{"HTTPS://ads.example/", "ads.example"},
		{"https://BÜCHER.ads.example/path", "xn--bcher-kva.ads.example"},
		{"  ads.example.  ", "ads.example"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			result, err := s.ExplainClientPolicy(ClientPolicyExplain{Name: tc.input})
			require.NoError(t, err)
			assert.Equal(t, tc.normalized, result.Normalized)
			assert.Equal(t, "block", string(result.Decision.Result))
		})
	}
}

func TestExplainInvalidNamesHaveHelpfulFieldErrors(t *testing.T) {
	s, _ := dhcpFixture(t)
	for _, input := range []string{"https://", "https:///path", "https://bad host.example/", "https://ads.example:bad/", "ftp://ads.example/", "not a domain", "a..example", `bad\256.example`} {
		t.Run(input, func(t *testing.T) {
			_, err := s.ExplainClientPolicy(ClientPolicyExplain{Name: input})
			var field *FieldError
			require.ErrorAs(t, err, &field)
			assert.Equal(t, []string{"name"}, field.Path)
			assert.Contains(t, field.Message, "Enter a valid domain or URL")
			assert.Contains(t, field.Message, "https://example.com")
			assert.NotContains(t, field.Message, "policy:")
		})
	}
}

func TestExplainUsesDecodedQuestionForLocalRouting(t *testing.T) {
	s, store := dhcpFixture(t)
	_, err := s.Mutate(t.Context(), "records", "POST", Mutation{Revision: store.Inspect().SavedRevision, Item: map[string]any{"name": "local.example", "type": "A", "value": "192.0.2.1", "ttl": 60}})
	require.NoError(t, err)
	result, err := s.ExplainClientPolicy(ClientPolicyExplain{Name: `\108ocal.example`})
	require.NoError(t, err)
	assert.Equal(t, "local.example", result.Normalized)
	assert.Equal(t, "local", result.Handling)
}

func TestExplainRootAndLongEscapedLabel(t *testing.T) {
	s, _ := dhcpFixture(t)
	for _, name := range []string{"", ".", strings.Repeat(`\000`, 63) + ".example"} {
		t.Run(name, func(t *testing.T) {
			result, err := s.ExplainClientPolicy(ClientPolicyExplain{Name: name})
			require.NoError(t, err)
			assert.Equal(t, strings.TrimSuffix(name, "."), result.Normalized)
		})
	}
}
