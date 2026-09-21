package stats

import (
	"sync"
	"time"
)

// Snapshot is a coherent cumulative process snapshot. Type and upstream IDs
// 0..255 have exact buckets; higher values have an exact shared Other bucket.
// Sequence counts terminal events, Version includes non-query counter updates.
type Snapshot struct {
	ObservedStart         int64 // Unix microseconds at collector creation.
	ObservedEnd           int64 // Exclusive observation bound at Snapshot, not event delivery.
	Version               uint64
	Sequence              uint64
	Admitted              uint64
	Rejected              uint64
	Malformed             uint64
	Outcomes              [OutcomeCount]uint64
	Types                 [257]uint64
	Upstreams             [257]uint64
	Coalesced             uint64
	FallbackUsed          uint64
	Truncated             uint64
	ResponsePolicyBlocked uint64
	Attempts              uint64
	Retries               uint64
	HealthProbes          uint64
	BackgroundRefreshes   uint64
	Dropped               uint64
}

type Collector struct {
	mu     sync.Mutex
	s      Snapshot
	events chan QueryEvent
}

func New(capacity int) *Collector {
	if capacity < 0 {
		capacity = 0
	}
	if capacity > 65536 {
		capacity = 65536
	}
	return &Collector{events: make(chan QueryEvent, capacity), s: Snapshot{ObservedStart: time.Now().UnixMicro()}}
}

func (c *Collector) Events() <-chan QueryEvent { return c.events }
func (c *Collector) Snapshot() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.s
	s.ObservedEnd = time.Now().UnixMicro()
	return s
}

func bucket(v uint32) int {
	if v > 255 {
		return 256
	}
	return int(v)
}

func (c *Collector) Record(e QueryEvent) {
	c.RecordWith(e, nil)
}

// RecordWith assigns the sequence and synchronously publishes bounded auxiliary
// metadata before the event can be consumed. The callback must not call Collector
// methods, retain request buffers, perform I/O, or block on a consumer.
func (c *Collector) RecordWith(e QueryEvent, beforePublish func(uint64)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e.Outcome >= OutcomeCount {
		e.Outcome = ResolutionError
	}
	c.s.Version++
	c.s.Sequence++
	e.Sequence = c.s.Sequence
	if beforePublish != nil {
		beforePublish(e.Sequence)
	}
	c.s.Outcomes[e.Outcome]++
	if e.Outcome == AdmissionRejected {
		c.s.Rejected++
	} else {
		c.s.Admitted++
	}
	c.s.Types[bucket(uint32(e.QType))]++
	c.s.Upstreams[bucket(e.UpstreamID)]++
	if e.Flags&FlagCoalesced != 0 {
		c.s.Coalesced++
	}
	if e.Flags&FlagFallbackUsed != 0 {
		c.s.FallbackUsed++
	}
	if e.Flags&FlagTruncated != 0 {
		c.s.Truncated++
	}
	if e.Flags&FlagResponsePolicyBlocked != 0 {
		c.s.ResponsePolicyBlocked++
	}
	select {
	case c.events <- e:
	default:
		c.s.Dropped++
	}
}

func (c *Collector) RecordMalformed() { c.mu.Lock(); c.s.Malformed++; c.s.Version++; c.mu.Unlock() }

// RecordAttempt counts exchanges, independently of terminal client requests.
func (c *Collector) RecordAttempt(retry, probe, refresh bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.s.Attempts++
	c.s.Version++
	if retry {
		c.s.Retries++
	}
	if probe {
		c.s.HealthProbes++
	}
	if refresh {
		c.s.BackgroundRefreshes++
	}
}
