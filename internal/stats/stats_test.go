package stats

import (
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLayout(t *testing.T) {
	var e QueryEvent
	assert.Equal(t, uintptr(320), unsafe.Sizeof(e))
	assert.Equal(t, uintptr(32), unsafe.Offsetof(e.QName))
	assert.Equal(t, uintptr(287), unsafe.Offsetof(e.QNameLength))
	assert.Equal(t, uintptr(312), unsafe.Offsetof(e.Sequence))
}

func TestCountersOverflowAndOwnership(t *testing.T) {
	c := New(1)
	e := QueryEvent{Outcome: PolicyBlock, QNameLength: 1}
	e.QName[0] = 42
	c.Record(e)
	e.QName[0] = 9
	for o := LocalAnswer; o < OutcomeCount; o++ {
		c.Record(QueryEvent{Outcome: o, Flags: FlagCoalesced | FlagFallbackUsed})
	}
	c.RecordMalformed()
	c.RecordAttempt(true, false, true)
	c.RecordAttempt(false, true, false)
	s := c.Snapshot()
	assert.Equal(t, uint64(7), s.Admitted)
	assert.Equal(t, uint64(1), s.Rejected)
	assert.Equal(t, uint64(7), s.Dropped)
	assert.Equal(t, uint64(2), s.Outcomes[PolicyBlock])
	assert.Equal(t, uint64(2), s.Attempts)
	assert.Equal(t, uint64(1), s.BackgroundRefreshes)
	assert.Equal(t, uint64(1), s.Malformed)
	assert.Equal(t, byte(42), (<-c.Events()).QName[0])
}

func TestConcurrentExact(t *testing.T) {
	c := New(0)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 1000 {
				c.Record(QueryEvent{Outcome: FreshCache})
			}
		})
	}
	wg.Wait()
	s := c.Snapshot()
	assert.Equal(t, uint64(8000), s.Admitted)
	assert.Equal(t, s.Admitted, s.Dropped)
}

func TestRollupUTCAndBound(t *testing.T) {
	a := NewAggregator(2, 1)
	start := time.Date(2026, 3, 8, 7, 0, 0, 0, time.UTC)
	e := QueryEvent{Timestamp: start.UnixMicro(), Outcome: PolicyBlock, QNameLength: 1}
	e.QName[0] = 1
	a.Add(e, true)
	e.QName[0] = 2
	a.Add(e, true)
	r := a.Rankings(start, start.Add(time.Minute))
	require.Len(t, r.BlockedDomains, 1)
	assert.False(t, r.Complete)
	assert.Equal(t, uint64(1), r.OtherDomains)
	rollups, err := a.Rollups(start, start.Add(time.Minute))
	require.NoError(t, err)
	require.Len(t, rollups, 1)
	assert.Equal(t, uint64(2), rollups[0].Outcomes[PolicyBlock])
	assert.False(t, rollups[0].Complete)
	assert.Equal(t, int64(-60000000), UTCBucket(-1, time.Minute))
	for i := 0; i < 20; i++ {
		e.Timestamp = start.Add(time.Duration(i) * time.Minute).UnixMicro()
		a.Add(e, true)
	}
	assert.LessOrEqual(t, len(a.buckets), 2)
}

func BenchmarkRecord(b *testing.B) {
	c := New(4096)
	e := QueryEvent{Outcome: FreshCache}
	b.ReportAllocs()
	for b.Loop() {
		c.Record(e)
	}
}

func BenchmarkRecordWithDelivery(b *testing.B) {
	c := New(1)
	e := QueryEvent{Outcome: FreshCache}
	b.ReportAllocs()
	for b.Loop() {
		c.Record(e)
		<-c.Events()
	}
}
