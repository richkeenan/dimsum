package storage

import (
	"testing"
	"time"

	"github.com/richkeenan/dimsum/internal/stats"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Removing retained detail while leaving its independently retained aggregates
// catches accidental whole-window detail reads as well as double counting.
func TestUnalignedAggregatesRetainInteriorAndExactEdges(t *testing.T) {
	d := openTest(t)
	start, end := testStart.Add(30*time.Minute+542*time.Millisecond), testStart.Add(150*time.Minute+542*time.Millisecond)
	var events []stats.QueryEvent
	for i, at := range []time.Time{start.Add(-time.Microsecond), start, testStart.Add(time.Hour), testStart.Add(2 * time.Hour), end.Add(-time.Microsecond), end} {
		e := event(uint64(i + 1))
		e.Timestamp = at.UnixMicro()
		events = append(events, e)
	}
	require.NoError(t, d.WriteBatch(t.Context(), "boot", events, BatchOptions{Snapshot: &stats.Snapshot{Version: 6, Sequence: 6, ObservedStart: testStart.UnixMicro(), ObservedEnd: testStart.Add(3 * time.Hour).UnixMicro()}}))
	_, err := d.write.Exec("DELETE FROM query_events WHERE timestamp>=? AND timestamp<?", testStart.Add(time.Hour).UnixMicro(), testStart.Add(2*time.Hour).UnixMicro())
	require.NoError(t, err)
	s, err := d.Summary(t.Context(), start, end)
	require.NoError(t, err)
	assert.Equal(t, uint64(4), s.Admitted)
	assert.Equal(t, uint64(4), s.Blocked)
	assert.Equal(t, uint64(2000), s.Duration)
	assert.True(t, s.Complete)
	r, err := d.Rankings(t.Context(), start, end)
	require.NoError(t, err)
	require.Len(t, r.Clients, 1)
	assert.Equal(t, uint64(4), r.Clients[0].Count)
	require.Len(t, r.BlockedDomains, 1)
	assert.Equal(t, uint64(4), r.BlockedDomains[0].Count)
	assert.True(t, r.Complete)

	// Logical retention hides the first edge even before physical deletion.
	_, err = d.write.Exec("UPDATE storage_meta SET value=? WHERE key IN ('detail_cutoff','minute_cutoff')", testStart.Add(time.Hour).UnixMicro())
	require.NoError(t, err)
	s, err = d.Summary(t.Context(), start, end)
	require.NoError(t, err)
	assert.Equal(t, uint64(3), s.Admitted)
	assert.False(t, s.Complete)
	r, err = d.Rankings(t.Context(), start, end)
	require.NoError(t, err)
	assert.Equal(t, uint64(3), r.Clients[0].Count)
	assert.False(t, r.Complete)
}

func TestSummaryKeepsDailyInteriorAfterFinerRetention(t *testing.T) {
	d := openTest(t)
	e := event(1)
	e.Timestamp = testStart.Add(36 * time.Hour).UnixMicro()
	require.NoError(t, d.WriteBatch(t.Context(), "boot", []stats.QueryEvent{e}))
	_, err := d.write.Exec("UPDATE storage_meta SET value=? WHERE key IN ('detail_cutoff','minute_cutoff','hour_cutoff')", testStart.Add(3*24*time.Hour).UnixMicro())
	require.NoError(t, err)
	s, err := d.Summary(t.Context(), testStart.Add(time.Second), testStart.Add(2*24*time.Hour+time.Second))
	require.NoError(t, err)
	assert.Equal(t, uint64(1), s.Admitted)
	assert.False(t, s.Complete)
}

func TestRankingsMergeAllKeysBeforeTopTen(t *testing.T) {
	d := openTest(t)
	var events []stats.QueryEvent
	for part, offset := range []time.Duration{45 * time.Minute, 90 * time.Minute, 135 * time.Minute} {
		for key := 0; key < 11; key++ {
			count, identity := 10, byte(part*10+key+1)
			if key == 10 {
				count, identity = 9, 200 // Eleventh in every part, first overall.
			}
			for range count {
				e := event(uint64(len(events) + 1))
				e.Timestamp = testStart.Add(offset).UnixMicro()
				e.Client[15], e.QName[1] = identity, identity
				events = append(events, e)
			}
		}
	}
	require.NoError(t, d.WriteBatch(t.Context(), "boot", events))
	r, err := d.Rankings(t.Context(), testStart.Add(30*time.Minute), testStart.Add(150*time.Minute))
	require.NoError(t, err)
	assert.Equal(t, uint64(31), r.ActiveClients)
	for _, rows := range [][]stats.Ranking{r.Clients, r.BlockedDomains} {
		require.Len(t, rows, 10)
		assert.Equal(t, uint64(27), rows[0].Count)
		assert.Contains(t, []byte(rows[0].Key), byte(200))
		for i := 1; i < 10; i++ {
			assert.Equal(t, uint64(10), rows[i].Count)
			if i > 1 {
				assert.Less(t, rows[i-1].Key, rows[i].Key)
			}
		}
	}
}

func TestSummaryMixedOutcomesAcrossTiers(t *testing.T) {
	d := openTest(t)
	start := testStart.Add(30*time.Second + 542*time.Millisecond)
	end := testStart.Add(49*time.Hour + 91*time.Second + 542*time.Millisecond)
	var events []stats.QueryEvent
	for i, offset := range []time.Duration{0, 45 * time.Second, 2 * time.Hour, 30 * time.Hour, 48 * time.Hour, 49 * time.Hour, 49*time.Hour + time.Minute} {
		e := event(uint64(i + 1))
		e.Timestamp, e.Outcome, e.Duration = start.Add(offset).UnixMicro(), stats.Outcome(i), uint32(i+1)
		events = append(events, e)
	}
	require.NoError(t, d.WriteBatch(t.Context(), "boot", events, BatchOptions{Snapshot: &stats.Snapshot{Version: 7, Sequence: 7, ObservedStart: start.UnixMicro(), ObservedEnd: end.UnixMicro()}}))
	s, err := d.Summary(t.Context(), start, end)
	require.NoError(t, err)
	assert.Equal(t, Summary{Admitted: 6, Blocked: 1, FreshCache: 1, StaleCache: 1, Rejected: 1, Duration: 21, Complete: true}, s)
	// A wholly sub-minute range must read exactly once, not overlap two edges.
	s, err = d.Summary(t.Context(), start, start.Add(time.Microsecond))
	require.NoError(t, err)
	assert.Equal(t, Summary{Admitted: 1, Duration: 1, Complete: true}, s)
}

func TestAggregatesFallBackWhenCoarseRetentionIsShorter(t *testing.T) {
	for _, expired := range []string{"hour_cutoff", "day_cutoff", "minute_cutoff"} {
		t.Run(expired, func(t *testing.T) {
			d := openTest(t)
			start, end := testStart.Add(time.Second), testStart.Add(3*24*time.Hour+time.Second)
			events := []stats.QueryEvent{event(1), event(2), event(3)}
			for i := range events {
				events[i].Timestamp = testStart.Add(time.Duration(i)*24*time.Hour + 90*time.Second).UnixMicro()
			}
			require.NoError(t, d.WriteBatch(t.Context(), "boot", events, BatchOptions{Snapshot: &stats.Snapshot{Version: 3, Sequence: 3, ObservedStart: start.UnixMicro(), ObservedEnd: end.UnixMicro()}}))
			// The first two days of this tier have expired, but finer history
			// remains. Counts and complete coverage must survive the fallback.
			_, err := d.write.Exec("UPDATE storage_meta SET value=? WHERE key=?", testStart.Add(2*24*time.Hour).UnixMicro(), expired)
			require.NoError(t, err)
			s, err := d.Summary(t.Context(), start, end)
			require.NoError(t, err)
			assert.Equal(t, uint64(3), s.Admitted)
			assert.True(t, s.Complete)
			r, err := d.Rankings(t.Context(), start, end)
			require.NoError(t, err)
			require.Len(t, r.Clients, 1)
			assert.Equal(t, uint64(3), r.Clients[0].Count)
			assert.Equal(t, uint64(1), r.ActiveClients)
			assert.True(t, r.Complete)
		})
	}
}
