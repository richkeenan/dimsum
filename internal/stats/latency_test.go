package stats

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLatencyQuantizationBounds(t *testing.T) {
	for _, v := range []struct{ value, lower, upper uint32 }{
		{0, 0, 0}, {15, 15, 15}, {16, 16, 16}, {31, 31, 31},
		{32, 32, 33}, {1000, 992, 1023}, {100000, 98304, 102399},
		{math.MaxUint32, 4160749568, math.MaxUint32},
	} {
		lo, hi := LatencyBounds(LatencyIndex(v.value))
		assert.Equal(t, v.lower, lo)
		assert.Equal(t, v.upper, hi)
	}
	// Every bin is contiguous and the midpoint respects the promised error bound.
	var last uint32
	for i := range (LatencyHistogram{}) {
		lo, hi := LatencyBounds(i)
		if i > 0 {
			assert.Equal(t, uint64(last)+1, uint64(lo))
		}
		assert.Equal(t, i, LatencyIndex(lo))
		assert.Equal(t, i, LatencyIndex(hi))
		assert.LessOrEqual(t, float64((hi-lo)/2), float64(lo)/32)
		last = hi
	}
	assert.Equal(t, uint32(math.MaxUint32), last)
}

func TestLatencyPercentileUsesMergedNearestRank(t *testing.T) {
	var h LatencyHistogram
	assert.Nil(t, h.Percentile(95))
	h[LatencyIndex(10)] = 99
	h[LatencyIndex(100000)] = 1
	for _, percentile := range []uint64{50, 95, 99} {
		got := h.Percentile(percentile)
		require.NotNil(t, got)
		assert.Equal(t, uint32(10), *got)
	}
	assert.Equal(t, uint32(100351), *h.Percentile(100))
	assert.Nil(t, h.Percentile(0))
	assert.Nil(t, h.Percentile(101))
	var zero LatencyHistogram
	zero[0] = 1
	assert.Equal(t, uint32(0), *zero.Percentile(99))
}
