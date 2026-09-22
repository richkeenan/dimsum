package storage

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/richkeenan/dimsum/internal/stats"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRuleHistoryBootIsolation(t *testing.T) {
	d := openTest(t)
	ctx := context.Background()
	a, b := event(1), event(1)
	b.Timestamp += int64(10 * 24 * time.Hour / time.Microsecond)
	require.NoError(t, d.WriteBatch(ctx, "old", []stats.QueryEvent{a}, BatchOptions{Rules: []RuleVersion{{1, 2, "old explanation", ""}}}))
	require.NoError(t, d.WriteBatch(ctx, "new", []stats.QueryEvent{b}, BatchOptions{Rules: []RuleVersion{{1, 2, "new explanation", ""}}}))
	p, err := d.Query(ctx, QueryOptions{Start: testStart, End: testStart.Add(11 * 24 * time.Hour)})
	require.NoError(t, err)
	require.Len(t, p.Rows, 2)
	assert.Equal(t, "new explanation", p.Rows[0].RuleDescription)
	assert.Equal(t, "old explanation", p.Rows[1].RuleDescription)
	require.NoError(t, d.Retain(ctx, testStart.Add(10*24*time.Hour), DefaultRetention()))
	var count int
	require.NoError(t, d.read.QueryRow("SELECT COUNT(*) FROM rule_versions WHERE boot_id='old'").Scan(&count))
	assert.Zero(t, count)
	p, err = d.Query(ctx, QueryOptions{Start: testStart, End: testStart.Add(11 * 24 * time.Hour)})
	require.NoError(t, err)
	require.Len(t, p.Rows, 1)
	assert.Equal(t, "new explanation", p.Rows[0].RuleDescription)
}

func TestMigrationV1AmbiguousRulesAndPendingSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v1.sqlite")
	raw, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	_, err = raw.Exec(initialSchema)
	require.NoError(t, err)
	_, err = raw.Exec(`INSERT INTO domains VALUES(1,x'00');INSERT INTO clients VALUES(1,zeroblob(16));INSERT INTO rule_versions VALUES(1,2,'ambiguous'),(1,3,'single boot');INSERT INTO writer_state(boot_id,event_watermark,snapshot_watermark,snapshot) VALUES('a',1,2,'{"Version":2,"Sequence":2}'),('b',1,0,NULL),('c',1,0,NULL)`)
	require.NoError(t, err)
	for _, v := range []struct {
		boot string
		rule int
	}{{"a", 2}, {"b", 2}, {"c", 3}} {
		_, err = raw.Exec(`INSERT INTO query_events(boot_id,sequence,timestamp,duration,domain_id,client_id,qtype,qclass,outcome,rcode,upstream_id,generation,rule_id,flags) VALUES(?,1,?,0,1,1,1,1,1,0,0,1,?,0)`, v.boot, testStart.UnixMicro(), v.rule)
		require.NoError(t, err)
	}
	require.NoError(t, raw.Close())
	d, err := Open(path)
	require.NoError(t, err)
	p, err := d.Query(context.Background(), QueryOptions{Start: testStart, End: testStart.Add(time.Hour)})
	require.NoError(t, err)
	require.Len(t, p.Rows, 3)
	assert.False(t, p.Complete)
	for _, r := range p.Rows {
		if r.Boot == "c" {
			assert.Equal(t, "single boot", r.RuleDescription)
		} else {
			assert.Empty(t, r.RuleDescription)
		}
	}
	var version, sequence int
	require.NoError(t, d.read.QueryRow("PRAGMA user_version").Scan(&version))
	assert.Equal(t, 4, version)
	require.NoError(t, d.read.QueryRow("SELECT snapshot_sequence FROM writer_state WHERE boot_id='a'").Scan(&sequence))
	assert.Equal(t, 2, sequence)
	require.NoError(t, d.Close())
	d, err = Open(path)
	require.NoError(t, err)
	require.NoError(t, d.Close())
}

func TestObservedCoverageAndPendingSnapshotAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "coverage.sqlite")
	d, err := Open(path)
	require.NoError(t, err)
	ctx := context.Background()
	s, err := d.Summary(ctx, testStart, testStart.Add(time.Hour))
	require.NoError(t, err)
	assert.False(t, s.Complete)
	series, err := d.Timeseries(ctx, testStart, testStart.Add(time.Hour), time.Hour)
	require.NoError(t, err)
	assert.False(t, series.Complete)
	future := time.Now().Add(time.Hour)
	s, err = d.Summary(ctx, future, future.Add(time.Hour))
	require.NoError(t, err)
	assert.False(t, s.Complete)
	snapshot := stats.Snapshot{Version: 2, Sequence: 2, Admitted: 2, ObservedStart: testStart.UnixMicro(), ObservedEnd: testStart.Add(time.Hour).UnixMicro()}
	require.NoError(t, d.WriteBatch(ctx, "pending", []stats.QueryEvent{event(1)}, BatchOptions{Snapshot: &snapshot}))
	s, err = d.Summary(ctx, testStart, testStart.Add(time.Hour))
	require.NoError(t, err)
	assert.False(t, s.Complete)
	require.NoError(t, d.Close())
	d, err = Open(path)
	require.NoError(t, err)
	defer d.Close()
	s, err = d.Summary(ctx, testStart, testStart.Add(time.Hour))
	require.NoError(t, err)
	assert.False(t, s.Complete)
	view, err := d.read.BeginTx(ctx, nil)
	require.NoError(t, err)
	var oldCount int
	require.NoError(t, view.QueryRow("SELECT COUNT(*) FROM query_events").Scan(&oldCount))
	assert.Equal(t, 1, oldCount)
	require.NoError(t, d.WriteBatch(ctx, "pending", []stats.QueryEvent{event(2)}))
	known, err := complete(ctx, view, testStart, testStart.Add(time.Hour), "detail_cutoff")
	require.NoError(t, err)
	assert.False(t, known, "old data cannot use newer progress to claim completeness")
	require.NoError(t, view.Rollback())
	s, err = d.Summary(ctx, testStart, testStart.Add(time.Hour))
	require.NoError(t, err)
	assert.True(t, s.Complete)
	assert.Equal(t, uint64(2), s.Admitted)
	s, err = d.Summary(ctx, testStart.Add(-time.Hour), testStart.Add(time.Hour))
	require.NoError(t, err)
	assert.False(t, s.Complete)
	s, err = d.Summary(ctx, testStart, testStart.Add(2*time.Hour))
	require.NoError(t, err)
	assert.False(t, s.Complete)
	// Idle observation can advance without changing query/counter version.
	snapshot.ObservedEnd = testStart.Add(2 * time.Hour).UnixMicro()
	require.NoError(t, d.WriteBatch(ctx, "pending", nil, BatchOptions{Snapshot: &snapshot}))
	s, err = d.Summary(ctx, testStart, testStart.Add(2*time.Hour))
	require.NoError(t, err)
	assert.True(t, s.Complete)
}

func TestDelayedEventsRespectEachRetentionTier(t *testing.T) {
	d := openTest(t)
	ctx := context.Background()
	now := testStart.Add(20 * 24 * time.Hour)
	require.NoError(t, d.Retain(ctx, now, DefaultRetention()))
	e := event(1)
	require.NoError(t, d.WriteBatch(ctx, "delayed", []stats.QueryEvent{e}))
	require.NoError(t, d.WriteBatch(ctx, "delayed", []stats.QueryEvent{e}))
	var count int
	for _, table := range []string{"query_events", "domains", "clients"} {
		require.NoError(t, d.read.QueryRow("SELECT COUNT(*) FROM "+table).Scan(&count))
		assert.Zero(t, count, table)
	}
	for _, v := range []struct{ resolution, want int }{{60, 0}, {3600, 1}, {86400, 1}} {
		require.NoError(t, d.read.QueryRow("SELECT COALESCE(SUM(count),0) FROM rollups WHERE resolution=?", v.resolution).Scan(&count))
		assert.Equal(t, v.want, count)
	}
	r, err := d.Rankings(ctx, testStart, testStart.Add(time.Hour))
	require.NoError(t, err)
	require.Len(t, r.Clients, 1)
	assert.Equal(t, uint64(1), r.Clients[0].Count)
	e.Sequence = 2
	e.Timestamp = now.Add(-100 * 24 * time.Hour).UnixMicro()
	require.NoError(t, d.WriteBatch(ctx, "delayed", []stats.QueryEvent{e}))
	require.NoError(t, d.read.QueryRow("SELECT SUM(count) FROM rollups WHERE resolution=86400").Scan(&count))
	assert.Equal(t, 2, count)
	require.NoError(t, d.read.QueryRow("SELECT SUM(count) FROM rollups WHERE resolution=3600").Scan(&count))
	assert.Equal(t, 1, count)
	e.Sequence = 3
	e.Timestamp = now.Add(-400 * 24 * time.Hour).UnixMicro()
	require.NoError(t, d.WriteBatch(ctx, "delayed", []stats.QueryEvent{e}))
	require.NoError(t, d.read.QueryRow("SELECT event_watermark FROM writer_state WHERE boot_id='delayed'").Scan(&count))
	assert.Equal(t, 3, count)
	require.NoError(t, d.read.QueryRow("SELECT SUM(count) FROM rollups WHERE resolution=86400").Scan(&count))
	assert.Equal(t, 2, count)
	snapshot := stats.Snapshot{Version: 3, Sequence: 3, ObservedStart: now.Add(-time.Hour).UnixMicro(), ObservedEnd: now.UnixMicro()}
	require.NoError(t, d.WriteBatch(ctx, "delayed", nil, BatchOptions{Snapshot: &snapshot}))
	s, err := d.Summary(ctx, testStart, testStart.Add(time.Hour))
	require.NoError(t, err)
	assert.False(t, s.Complete, "delayed events cannot extend observed coverage backwards")
}

func TestRetentionCatchupYieldsToIngestion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "backlog.sqlite")
	d, err := Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, d.Close()) })
	ctx := context.Background()
	now := time.Now()
	old := now.Add(-10 * 24 * time.Hour).UnixMicro()
	for base := 0; base < 1536; base += 512 {
		batch := make([]stats.QueryEvent, 512)
		for i := range batch {
			batch[i] = event(uint64(base + i + 1))
			batch[i].Timestamp = old
		}
		require.NoError(t, d.WriteBatch(ctx, "expired", batch))
	}
	require.NoError(t, d.Retain(ctx, now, DefaultRetention()))
	assert.True(t, d.Status().Backlogged)
	var remaining int
	require.NoError(t, d.read.QueryRow("SELECT COUNT(*) FROM query_events").Scan(&remaining))
	assert.Equal(t, 1024, remaining)
	require.NoError(t, d.Close())
	d, err = Open(path)
	require.NoError(t, err)
	assert.True(t, d.Status().Backlogged, "unfinished maintenance survives restart")
	c := stats.New(512)
	live := event(0)
	live.Timestamp = now.UnixMicro()
	c.Record(live)
	run, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- d.Run(run, c, "live") }()
	defer cancel()
	require.Eventually(t, func() bool {
		var oldCount, liveCount int
		err := d.read.QueryRow("SELECT COUNT(*) FROM query_events WHERE boot_id='expired'").Scan(&oldCount)
		if err != nil {
			return false
		}
		err = d.read.QueryRow("SELECT COUNT(*) FROM query_events WHERE boot_id='live'").Scan(&liveCount)
		return err == nil && oldCount == 0 && liveCount == 1 && !d.Status().Backlogged
	}, 3*time.Second, 10*time.Millisecond)
	cancel()
	require.NoError(t, <-done)
	// Configuration's independently approved maximum remains intact.
	r := DefaultRetention()
	r.Day = 3650 * 24 * time.Hour
	require.NoError(t, d.SetRetention(r))
}
