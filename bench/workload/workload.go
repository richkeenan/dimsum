// Package workload generates indexed synthetic DNS arrivals without retaining a trace.
package workload

import (
	"crypto/sha256"
	"fmt"
	"time"
)

const MaxQueries = 100_000

// Spec defines generator version 1. Rate is queries per second. All dimensions
// are synthetic: household is a QTYPE approximation, not a captured replay.
type Spec struct {
	Family string `json:"family"`
	Seed   uint64 `json:"seed"`
	Count  int    `json:"count"`
	Rate   int    `json:"rate"`
}

type Query struct {
	Index  int
	Offset time.Duration
	Name   string // absolute presentation name under .test
	Type   uint16
}

type Workload struct{ spec Spec }

func New(s Spec) (Workload, error) {
	if s.Family != "household" && s.Family != "hot-key" && s.Family != "churn" {
		return Workload{}, fmt.Errorf("unknown workload family %q", s.Family)
	}
	if s.Count < 1 || s.Count > MaxQueries || s.Rate < 1 || s.Rate > 1_000_000_000 {
		return Workload{}, fmt.Errorf("count or rate outside workload bounds")
	}
	if time.Duration(int64(s.Count-1)*int64(time.Second)/int64(s.Rate)) > 10*time.Minute {
		return Workload{}, fmt.Errorf("offered schedule exceeds ten minutes")
	}
	return Workload{spec: s}, nil
}

func (w Workload) Spec() Spec { return w.spec }

// At is independent of call order. Its index must be in [0, Count).
func (w Workload) At(i int) Query {
	if i < 0 || i >= w.spec.Count {
		panic("workload index out of range")
	}
	q := Query{Index: i, Offset: time.Duration(int64(i) * int64(time.Second) / int64(w.spec.Rate)), Type: 1}
	x := mix(w.spec.Seed + uint64(i))
	switch w.spec.Family {
	case "hot-key":
		q.Name = "hot.bench.test."
	case "churn":
		q.Name = fmt.Sprintf("r%016x-%d.bench.test.", x, i)
	case "household":
		// A permutation of each 100 arrivals gives exact 67/14/17/1/1
		// percentages per complete block, without long QTYPE runs.
		bucket := (uint64(i)*37 + w.spec.Seed%100) % 100
		switch {
		case bucket < 67:
			q.Type = 1
		case bucket < 81:
			q.Type = 28
		case bucket < 98:
			q.Type = 65
		case bucket == 98:
			q.Type = 12
		default:
			q.Type = 64
		}
		key := (x>>8)%256 + 8
		if x%10 < 8 {
			key = (x >> 8) % 8
		}
		q.Name = fmt.Sprintf("host%03d.bench.test.", key)
	}
	return q
}

// Digest hashes canonical UTF-8 TSV records: index, offset in nanoseconds,
// absolute name, numeric QTYPE, followed by a newline. No platform RNG is used.
func (w Workload) Digest() string {
	h := sha256.New()
	for i := range w.spec.Count {
		q := w.At(i)
		fmt.Fprintf(h, "%d\t%d\t%s\t%d\n", q.Index, q.Offset, q.Name, q.Type)
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

// SplitMix64 finalizer, pinned as part of generator version 1.
func mix(x uint64) uint64 {
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}
