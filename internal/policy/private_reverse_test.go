package policy

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrivateReverseBoundariesAndAllocations(t *testing.T) {
	zones := []string{"10.in-addr.arpa", "127.in-addr.arpa", "168.192.in-addr.arpa", "254.169.in-addr.arpa", "0.in-addr.arpa", "c.f.ip6.arpa", "d.f.ip6.arpa", "8.e.f.ip6.arpa", "9.e.f.ip6.arpa", "a.e.f.ip6.arpa", "b.e.f.ip6.arpa"}
	for i := 16; i <= 31; i++ {
		zones = append(zones, fmt.Sprintf("%d.172.in-addr.arpa", i))
	}
	for _, zone := range zones {
		for _, text := range []string{zone, "child." + zone, "other" + zone, zone + ".example"} {
			n, err := NormalizeName(text)
			require.NoError(t, err)
			want := false
			for _, suffix := range zones {
				want = want || text == suffix || strings.HasSuffix(text, "."+suffix)
			}
			assert.Equal(t, want, PrivateReverse(n), text)
		}
	}
	for _, text := range []string{strings.Repeat("0.", 32) + "ip6.arpa", "1." + strings.Repeat("0.", 31) + "ip6.arpa"} {
		n, err := NormalizeName(text)
		require.NoError(t, err)
		assert.True(t, PrivateReverse(n))
		n, err = NormalizeName("child." + text)
		require.NoError(t, err)
		assert.False(t, PrivateReverse(n))
	}
	// Bytes resembling encoded labels inside a single label are not a suffix.
	encoded := []byte("\x0710.in-a\x02rp\x01a\x00")
	n, err := NameFromWire(encoded)
	require.NoError(t, err)
	assert.False(t, PrivateReverse(n))
	for _, text := range []string{"www.example.test", "1.0.0.10.in-addr.arpa", "15.172.in-addr.arpa", "32.172.in-addr.arpa"} {
		n, err := NormalizeName(text)
		require.NoError(t, err)
		var result bool
		allocs := testing.AllocsPerRun(100, func() { result = PrivateReverse(n) })
		assert.Zero(t, allocs, text)
		assert.Equal(t, text == "1.0.0.10.in-addr.arpa", result)
	}
}
