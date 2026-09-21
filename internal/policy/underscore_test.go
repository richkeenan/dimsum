package policy_test

import (
	"strings"
	"testing"

	"github.com/richkeenan/dimsum/internal/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDNSUnderscoreNormalization(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"_SIP._TCP.Example.", "_sip._tcp.example"},
		{"philadelphia_cbslocal.us.intellitxt.com", "philadelphia_cbslocal.us.intellitxt.com"},
		{"_SERVICE。BÜCHER.Example。", "_service.xn--bcher-kva.example"},
		{"a_b-c.example", "a_b-c.example"},
		{strings.Repeat("_", 63) + ".example", strings.Repeat("_", 63) + ".example"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			n, err := policy.NormalizeName(tc.input)
			require.NoError(t, err)
			assert.Equal(t, tc.want, n.Display())
			again, err := policy.NormalizeName(n.Display())
			require.NoError(t, err)
			assert.Equal(t, n, again)
		})
	}
	for _, s := range []string{"\x80", "_srv.\x80", "_srv..example", "_srv.example..", "_srv.example。.", "*._srv.example", "_s?r.example", "_s/r.example", "_s r.example", "_s\x00r.example", "-a_.example", "a_-.example", "xn--bad_.example", "_bücher.example", "xn--ildcard-0c2c.facture-rapide.fr", strings.Repeat("_", 64) + ".example", strings.Repeat("_a.", 85)} {
		t.Run("invalid/"+s, func(t *testing.T) { _, err := policy.NormalizeName(s); assert.Error(t, err) })
	}
}

func TestDNSUnderscorePolicyForms(t *testing.T) {
	for _, tc := range []struct {
		k                policy.Kind
		pattern, yes, no string
	}{
		{policy.Exact, "ADS_TRACK.Example", "ads_track.example", "child.ads_track.example"},
		{policy.Suffix, "ADS_TRACK.Example", "child.ads_track.example", "notads_track.example"},
		{policy.Glob, "_S?P._TCP.BÜCHER.Example", "_sip._tcp.xn--bcher-kva.example", "_sipp._tcp.xn--bcher-kva.example"},
		{policy.Glob, "ads*._TRACK.Example", "ads123._track.example", "ads123.track.example"},
		{policy.Wildcard, "*._TRACK.Example", "a.b._track.example", "_track.example"},
	} {
		t.Run(string(tc.k)+tc.pattern, func(t *testing.T) {
			m, err := policy.Compile(1, []policy.Rule{{ID: "r", Kind: tc.k, Class: policy.SubscriptionDeny, Pattern: tc.pattern}}, policy.DefaultLimits())
			require.NoError(t, err)
			assert.Equal(t, policy.Block, m.Match(name(t, tc.yes)).Result)
			assert.Equal(t, policy.Forward, m.Match(name(t, tc.no)).Result)
		})
	}
}
