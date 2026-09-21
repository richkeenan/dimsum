package stats

import (
	"errors"
	"sort"
	"sync"
	"time"
)

// Aggregator is a consumer-side bounded recent-minute view. Overflow and missing
// delivery are explicit; it never silently treats a partial ranking as exact.
type Aggregator struct {
	mu         sync.Mutex
	minutes    int
	dimensions int
	latest     int64
	buckets    map[int64]*Bucket
}

type Bucket struct {
	Start        int64
	Total        uint64
	Blocked      uint64
	Duration     uint64
	Complete     bool
	Clients      map[string]uint64
	Domains      map[string]uint64
	OtherClients uint64
	OtherDomains uint64
	Outcomes     [OutcomeCount]uint64
	Histogram    [8]uint64
}

type Rollup struct {
	Start                    int64
	Total, Blocked, Duration uint64
	Outcomes                 [OutcomeCount]uint64
	Histogram                [8]uint64
	Complete                 bool
}

// Rollups returns owned value snapshots for a bounded, minute-aligned UTC window.
// A missing minute is explicitly incomplete rather than a claimed exact zero.
func (a *Aggregator) Rollups(start, end time.Time) ([]Rollup, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	s, e := start.UnixMicro(), end.UnixMicro()
	if e <= s || e-s > int64(a.minutes)*time.Minute.Microseconds() || UTCBucket(s, time.Minute) != s || UTCBucket(e, time.Minute) != e {
		return nil, errors.New("invalid minute window")
	}
	r := make([]Rollup, 0, (e-s)/time.Minute.Microseconds())
	for t := s; t < e; t += time.Minute.Microseconds() {
		v := Rollup{Start: t}
		if b := a.buckets[t]; b != nil {
			v.Total = b.Total
			v.Blocked = b.Blocked
			v.Duration = b.Duration
			v.Outcomes = b.Outcomes
			v.Histogram = b.Histogram
			v.Complete = b.Complete
		}
		r = append(r, v)
	}
	return r, nil
}

type Ranking struct {
	Key   string
	Count uint64
}
type Rankings struct {
	Clients, BlockedDomains    []Ranking
	Complete                   bool
	OtherClients, OtherDomains uint64
}

func NewAggregator(minutes, dimensions int) *Aggregator {
	if minutes < 1 {
		minutes = 1
	}
	if minutes > 10080 {
		minutes = 10080
	}
	if dimensions < 1 {
		dimensions = 1
	}
	if dimensions > 4096 {
		dimensions = 4096
	}
	return &Aggregator{minutes: minutes, dimensions: dimensions, buckets: make(map[int64]*Bucket)}
}

// UTCBucket also floors negative Unix times correctly.
func UTCBucket(micros int64, width time.Duration) int64 {
	w := width.Microseconds()
	if w <= 0 {
		panic("nonpositive bucket")
	}
	r := micros % w
	if r < 0 {
		r += w
	}
	return micros - r
}

func (a *Aggregator) Add(e QueryEvent, complete bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	t := UTCBucket(e.Timestamp, time.Minute)
	if len(a.buckets) == 0 || t > a.latest {
		a.latest = t
	}
	cutoff := a.latest - int64(a.minutes-1)*time.Minute.Microseconds()
	for k := range a.buckets {
		if k < cutoff {
			delete(a.buckets, k)
		}
	}
	if t < cutoff {
		return
	}
	b := a.buckets[t]
	if b == nil {
		b = &Bucket{Start: t, Complete: true, Clients: map[string]uint64{}, Domains: map[string]uint64{}}
		a.buckets[t] = b
	}
	b.Complete = b.Complete && complete
	if e.Outcome >= OutcomeCount {
		b.Complete = false
		return
	}
	b.Outcomes[e.Outcome]++
	b.Histogram[HistogramIndex(e.Duration)]++
	if e.Outcome == AdmissionRejected {
		return
	}
	b.Total++
	b.Duration += uint64(e.Duration)
	add := func(m map[string]uint64, k string, other *uint64) {
		if _, ok := m[k]; ok || len(m) < a.dimensions {
			m[k]++
		} else {
			(*other)++
			b.Complete = false
		}
	}
	add(b.Clients, string(e.Client[:]), &b.OtherClients)
	if e.Outcome == PolicyBlock {
		b.Blocked++
		add(b.Domains, e.Domain(), &b.OtherDomains)
	}
}

// Rankings accepts minute-aligned half-open UTC windows. Missing buckets are
// incomplete, including minutes with no events (absence is not proof of zero).
func (a *Aggregator) Rankings(start, end time.Time) Rankings {
	a.mu.Lock()
	defer a.mu.Unlock()
	r := Rankings{Complete: true}
	clients, domains := map[string]uint64{}, map[string]uint64{}
	s, e := start.UnixMicro(), end.UnixMicro()
	if e <= s || e-s > int64(a.minutes)*time.Minute.Microseconds() || UTCBucket(s, time.Minute) != s || UTCBucket(e, time.Minute) != e {
		r.Complete = false
		return r
	}
	for t := s; t < e; t += time.Minute.Microseconds() {
		b := a.buckets[t]
		if b == nil {
			r.Complete = false
			continue
		}
		r.Complete = r.Complete && b.Complete
		r.OtherClients += b.OtherClients
		r.OtherDomains += b.OtherDomains
		for k, v := range b.Clients {
			clients[k] += v
		}
		for k, v := range b.Domains {
			domains[k] += v
		}
	}
	r.Clients = top(clients)
	r.BlockedDomains = top(domains)
	return r
}

func top(m map[string]uint64) []Ranking {
	r := make([]Ranking, 0, len(m))
	for k, v := range m {
		r = append(r, Ranking{k, v})
	}
	sort.Slice(r, func(i, j int) bool {
		if r[i].Count != r[j].Count {
			return r[i].Count > r[j].Count
		}
		return r[i].Key < r[j].Key
	})
	if len(r) > 10 {
		r = r[:10]
	}
	return r
}
