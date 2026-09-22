package storage

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/richkeenan/dimsum/internal/stats"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPerformanceWeightedPercentilesReplayAndOutcomes(t *testing.T) {
	d := openTest(t)
	var events []stats.QueryEvent
	for i := uint64(1); i <= 101; i++ {
		e := event(i)
		e.Duration, e.Outcome = 10, stats.FreshCache
		if i == 100 {
			e.Duration, e.Outcome = 100000, stats.ForwardedAnswer
			e.Timestamp += time.Minute.Microseconds()
		}
		if i == 101 {
			e.Duration, e.Outcome = 4000000, stats.AdmissionRejected
		}
		events = append(events, e)
	}
	snapshot := stats.Snapshot{Version: 1, Sequence: 101, ObservedStart: testStart.UnixMicro(), ObservedEnd: testStart.Add(3 * time.Minute).UnixMicro()}
	for range 2 {
		require.NoError(t, d.WriteBatch(t.Context(), "boot", events, BatchOptions{Snapshot: &snapshot}))
	}
	r, err := d.Performance(t.Context(), testStart, testStart.Add(3*time.Minute), time.Minute)
	require.NoError(t, err)
	assert.True(t, r.Complete)
	assert.Equal(t, uint64(100), r.Summary.Count)
	assert.Equal(t, uint64(100990), r.Summary.Duration)
	assert.Equal(t, uint64(100), r.Summary.Fine.Count())
	assert.Equal(t, uint32(10), *r.Summary.Fine.Percentile(99))
	assert.Equal(t, uint64(99), r.Outcomes[stats.FreshCache].Count)
	assert.Equal(t, uint64(1), r.Outcomes[stats.ForwardedAnswer].Count)
	require.Len(t, r.Points, 3)
	assert.Equal(t, uint64(99), r.Points[0].Count)
	assert.Equal(t, uint64(0), r.Points[2].Count)
	assert.True(t, r.Points[2].Complete, "observed idle is not missing data")
}

func TestPerformancePartialEdgesAndCoverage(t *testing.T) {
	d := openTest(t)
	var events []stats.QueryEvent
	for i, seconds := range []int{0, 30, 60, 90, 120} {
		e := event(uint64(i + 1))
		e.Timestamp += int64(seconds) * time.Second.Microseconds()
		e.Duration = uint32(seconds)
		events = append(events, e)
	}
	s := stats.Snapshot{Version: 1, Sequence: 5, ObservedStart: testStart.UnixMicro(), ObservedEnd: testStart.Add(2 * time.Minute).UnixMicro()}
	require.NoError(t, d.WriteBatch(t.Context(), "b", events, BatchOptions{Snapshot: &s}))
	r, err := d.Performance(t.Context(), testStart.Add(30*time.Second), testStart.Add(90*time.Second), time.Minute)
	require.NoError(t, err)
	assert.Equal(t, uint64(2), r.Summary.Count)
	assert.Equal(t, uint64(90), r.Summary.Duration)
	assert.True(t, r.Complete)
	require.Len(t, r.Points, 2)
	assert.Equal(t, testStart.Add(30*time.Second).UnixMicro(), r.Points[0].Timestamp)
	r, err = d.Performance(t.Context(), testStart, testStart.Add(4*time.Minute), time.Minute)
	require.NoError(t, err)
	assert.True(t, r.Points[0].Complete)
	assert.False(t, r.Points[2].Complete)
	assert.False(t, r.Points[3].Complete)
	assert.Zero(t, r.Points[3].Count)
	assert.False(t, r.Complete)
	for _, width := range []time.Duration{time.Second, 2 * time.Minute} {
		_, err = d.Performance(t.Context(), testStart, testStart.Add(time.Hour), width)
		assert.Error(t, err)
	}
	_, err = d.Performance(t.Context(), testStart, testStart.Add(48*time.Hour), time.Minute)
	assert.Error(t, err)
}

func TestPerformanceRetentionKeepsFineRollupsAndCascades(t *testing.T) {
	d := openTest(t)
	require.NoError(t, d.WriteBatch(t.Context(), "b", []stats.QueryEvent{event(1)}))
	now := testStart.Add(2 * time.Hour)
	r := Retention{time.Minute, time.Minute, 24 * time.Hour, 24 * time.Hour}
	require.NoError(t, d.Retain(t.Context(), now, r))
	p, err := d.Performance(t.Context(), testStart, testStart.Add(time.Hour), time.Hour)
	require.NoError(t, err)
	assert.Equal(t, uint64(1), p.Summary.Fine.Count())
	assert.Equal(t, uint64(1), p.Summary.Count)
	var n int
	require.NoError(t, d.read.QueryRow("SELECT COUNT(*) FROM latency_bins WHERE resolution=60").Scan(&n))
	assert.Zero(t, n)
	r = Retention{time.Minute, time.Minute, time.Minute, time.Minute}
	require.NoError(t, d.Retain(t.Context(), testStart.Add(48*time.Hour), r))
	require.NoError(t, d.read.QueryRow("SELECT COUNT(*) FROM latency_bins").Scan(&n))
	assert.Zero(t, n)
}

func TestPerformanceV3MigrationDoesNotInventPrecision(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.sqlite")
	raw, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	_, err = raw.Exec(initialSchema + bootCoverageSchema + ruleSourceSchema)
	require.NoError(t, err)
	_, err = raw.Exec("INSERT INTO rollups VALUES(60,?,2,1,500,0,0,1,0,0,0,0,0)", testStart.UnixMicro())
	require.NoError(t, err)
	require.NoError(t, raw.Close())
	d, err := Open(path)
	require.NoError(t, err)
	defer d.Close()
	p, err := d.Performance(t.Context(), testStart, testStart.Add(time.Minute), time.Minute)
	require.NoError(t, err)
	assert.Equal(t, uint64(1), p.Summary.Count)
	assert.Equal(t, uint64(500), p.Summary.Duration)
	assert.Equal(t, uint64(1), p.Summary.Histogram[2])
	assert.Zero(t, p.Summary.Fine.Count())
	assert.Nil(t, p.Summary.Fine.Percentile(95))
	require.NoError(t, d.WriteBatch(t.Context(), "new", []stats.QueryEvent{event(1)}))
	p, err = d.Performance(t.Context(), testStart, testStart.Add(time.Minute), time.Minute)
	require.NoError(t, err)
	assert.Equal(t, uint64(2), p.Summary.Count)
	assert.Equal(t, uint64(1), p.Summary.Fine.Count())
}
