package policy_test

import (
	"testing"

	"github.com/richkeenan/dimsum/internal/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWholeDomainBidi(t *testing.T) {
	for _, rtl := range []string{"א", "xn--4db"} {
		for _, input := range []string{
			"123." + rtl + ".example",
			rtl + ".123.example",
			"123.a_b." + rtl + ".example",
			"123_abc." + rtl + ".example",
			"_srv." + rtl + ".example",
			"abc_." + rtl + ".example",
		} {
			t.Run("reject/"+input, func(t *testing.T) {
				_, err := policy.NormalizeName(input)
				assert.Error(t, err, "RTL makes Bidi validation domain-wide")
				for _, kind := range []policy.Kind{policy.Exact, policy.Suffix} {
					_, err := policy.Compile(1, []policy.Rule{{ID: "bidi", Kind: kind, Class: policy.SubscriptionDeny, Pattern: input}}, policy.DefaultLimits())
					assert.Error(t, err)
				}
			})
		}
		for _, prefix := range []string{"abc", "a123", "a_b", "a_123"} {
			input := prefix + "." + rtl + ".example"
			t.Run("accept/"+input, func(t *testing.T) {
				n, err := policy.NormalizeName(input)
				require.NoError(t, err)
				assert.Equal(t, prefix+".xn--4db.example", n.Display())
			})
		}
	}
	for _, input := range []string{"123.example", "123_abc.example", "_srv.bücher.example", "abc_.example"} {
		_, err := policy.NormalizeName(input)
		assert.NoError(t, err, "non-RTL DNS names remain supported: %s", input)
	}
}
