package stats

import "math/bits"

// LatencyHistogram is a mergeable consumer-side distribution of integer
// microseconds. Sixteen subdivisions per power of two bound midpoint error to
// 3.125%; values below 32 microseconds are exact. It covers all uint32 durations.
type LatencyHistogram [464]uint64

func LatencyIndex(micros uint32) int {
	if micros < 32 {
		return int(micros)
	}
	shift := bits.Len32(micros) - 5
	return shift*16 + int(micros>>shift)
}

// LatencyBounds returns inclusive integer bounds for a valid bin index.
func LatencyBounds(index int) (uint32, uint32) {
	if index < 16 {
		return uint32(index), uint32(index)
	}
	shift := (index - 16) / 16
	lower := uint64(16+(index-16)%16) << shift
	upper := (uint64(17+(index-16)%16) << shift) - 1
	return uint32(lower), uint32(upper)
}

func (h *LatencyHistogram) Count() uint64 {
	var n uint64
	for _, count := range h {
		n += count
	}
	return n
}

// Percentile estimates the nearest-rank percentile using its bin midpoint.
// A nil result means no observations or an unsupported percentile.
func (h *LatencyHistogram) Percentile(percent uint64) *uint32 {
	n := h.Count()
	if n == 0 || percent == 0 || percent > 100 {
		return nil
	}
	// ceil(n*percent/100), without multiplying the full count.
	rank := n/100*percent + (n%100*percent+99)/100
	var seen uint64
	for i, count := range h {
		seen += count
		if seen >= rank {
			lo, hi := LatencyBounds(i)
			value := lo + (hi-lo)/2
			return &value
		}
	}
	return nil
}
