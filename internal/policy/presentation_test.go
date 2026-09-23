package policy

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestObservedNameRoundTripsEveryByte(t *testing.T) {
	for b := 0; b < 256; b++ {
		original, err := NameFromWire([]byte{1, byte(b), 0})
		require.NoError(t, err)
		parsed, err := ParseObservedName(original.Display())
		require.NoError(t, err, "byte %d", b)
		assert.Equal(t, original, parsed, "byte %d", b)
	}
}

func TestObservedNameBoundaries(t *testing.T) {
	maximum := strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 61)
	for _, input := range []string{"", ".", "_svc._tcp.example.", maximum, "BÜCHER.example"} {
		_, err := ParseObservedName(input)
		assert.NoError(t, err, "%q", input)
	}
	for _, input := range []string{"..", "a..b", "a..", `a\`, `a\12`, `a\1x2`, `a\256`, "a b", "a/b", "a\x00b", strings.Repeat("a", 64), maximum + "e", `bücher\046example`, string([]byte{255})} {
		_, err := ParseObservedName(input)
		assert.Error(t, err, "%q", input)
	}
	// Observed DNS labels must not weaken validation of configured hostnames.
	_, err := NormalizeName("r1---edge.example")
	assert.Error(t, err)
}
