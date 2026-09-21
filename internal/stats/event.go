// Package stats owns bounded observability values. Record never waits for a consumer.
package stats

import "math"

type Outcome uint16

const (
	LocalAnswer Outcome = iota
	PolicyBlock
	FreshCache
	StaleCache
	ForwardedAnswer
	ResolutionError
	AdmissionRejected
	OutcomeCount
)

const (
	FlagTCP uint32 = 1 << iota
	FlagCoalesced
	FlagFallbackUsed
	FlagTruncated
	FlagResponsePolicyBlocked
)

// QueryEvent is exactly 320 bytes on amd64/arm64. QName contains canonical DNS
// length-prefixed wire bytes, including the root terminator, not presentation text.
// Client is netip.Addr.As16 (IPv4 addresses use IPv4-mapped IPv6). Reserved is zero.
// Sequence is assigned by Collector.Record; callers must not reuse a boot ID.
type QueryEvent struct {
	Timestamp   int64
	Duration    uint32
	Generation  uint32
	Client      [16]byte
	QName       [255]byte
	QNameLength uint8
	QType       uint16
	QClass      uint16
	Outcome     Outcome
	RCode       uint16
	UpstreamID  uint32
	RuleID      uint32
	Flags       uint32
	Reserved    uint32
	Sequence    uint64
}

func SaturateMicros(micros uint64) uint32 {
	if micros > math.MaxUint32 {
		return math.MaxUint32
	}
	return uint32(micros)
}

// Domain returns a byte-safe canonical identity for consumer-side use.
func (e QueryEvent) Domain() string { return string(e.QName[:e.QNameLength]) }

// HistogramIndex uses microsecond boundaries 100,500,1000,5000,10000,
// 100000,1000000,+Inf, with exclusive upper bounds.
func HistogramIndex(micros uint32) int {
	i := 0
	for _, bound := range [...]uint32{100, 500, 1000, 5000, 10000, 100000, 1000000} {
		if micros >= bound {
			i++
		}
	}
	return i
}
