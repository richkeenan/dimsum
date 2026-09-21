package storage

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/richkeenan/dimsum/internal/stats"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testStart = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

func openTest(t *testing.T) *DB {
	t.Helper()
	d, err := Open(filepath.Join(t.TempDir(), "history.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, d.Close()) })
	return d
}
func event(seq uint64) stats.QueryEvent {
	e := stats.QueryEvent{Timestamp: testStart.UnixMicro(), Sequence: seq, Outcome: stats.PolicyBlock, Duration: 500, Generation: 1, RuleID: 2, QType: 1, QClass: 1, QNameLength: 3}
	copy(e.QName[:], []byte{1, 'a', 0})
	e.Client[15] = 1
	return e
}

func TestWriterReplayAndIndependentSnapshot(t *testing.T) {
	d := openTest(t)
	ctx := context.Background()
	events := []stats.QueryEvent{event(1), event(2)}
	s := stats.Snapshot{Version: 4, Sequence: 4, Admitted: 4, Dropped: 2}
	o := BatchOptions{Snapshot: &s, Rules: []RuleVersion{{1, 2, "block a", ""}}, Aliases: map[uint64][]byte{1: {1, 'b', 0}}}
	require.NoError(t, d.WriteBatch(ctx, "boot", events, o))
	require.NoError(t, d.WriteBatch(ctx, "boot", events, o))
	small := stats.Snapshot{Version: 1, Admitted: 1}
	require.NoError(t, d.WriteBatch(ctx, "boot", nil, BatchOptions{Snapshot: &small}))
	saved, err := d.Counters(ctx, "boot")
	require.NoError(t, err)
	assert.Equal(t, s, saved)
	summary, err := d.Summary(ctx, testStart, testStart.Add(time.Hour))
	require.NoError(t, err)
	assert.Equal(t, uint64(2), summary.Admitted)
	assert.False(t, summary.Complete)
	series, err := d.Timeseries(ctx, testStart, testStart.Add(time.Hour), time.Hour)
	require.NoError(t, err)
	require.Len(t, series.Points, 1)
	assert.Equal(t, uint64(2), series.Points[0].Outcomes[stats.PolicyBlock])
	assert.Equal(t, uint64(2), series.Points[0].Histogram[2])
	p, err := d.Query(ctx, QueryOptions{Start: testStart, End: testStart.Add(time.Hour)})
	require.NoError(t, err)
	require.Len(t, p.Rows, 2)
	assert.Equal(t, "block a", p.Rows[1].RuleDescription)
	assert.Equal(t, []byte{1, 'b', 0}, p.Rows[1].Alias)
}

func TestWriterRollbackImmutableAndCommitRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	d, err := Open(path)
	require.NoError(t, err)
	ctx := context.Background()
	require.NoError(t, d.WriteBatch(ctx, "b", []stats.QueryEvent{event(1)}, BatchOptions{Rules: []RuleVersion{{1, 2, "original", ""}}}))
	err = d.WriteBatch(ctx, "b", []stats.QueryEvent{event(2)}, BatchOptions{Rules: []RuleVersion{{1, 2, "changed", ""}}})
	require.Error(t, err)
	// Inject failure after detail insertion, at rollup write, to test atomicity.
	_, err = d.write.Exec(`CREATE TRIGGER fail_rollup BEFORE INSERT ON rollups BEGIN SELECT RAISE(ABORT,'fixture'); END`)
	require.NoError(t, err)
	require.Error(t, d.WriteBatch(ctx, "b", []stats.QueryEvent{event(2)}))
	var count, watermark int
	require.NoError(t, d.write.QueryRow("SELECT COUNT(*) FROM query_events").Scan(&count))
	assert.Equal(t, 1, count)
	require.NoError(t, d.write.QueryRow("SELECT event_watermark FROM writer_state WHERE boot_id='b'").Scan(&watermark))
	assert.Equal(t, 1, watermark)
	_, err = d.write.Exec("DROP TRIGGER fail_rollup")
	require.NoError(t, err)
	require.NoError(t, d.WriteBatch(ctx, "b", []stats.QueryEvent{event(2)}))
	require.NoError(t, d.Close())
	d, err = Open(path)
	require.NoError(t, err)
	defer d.Close()
	require.NoError(t, d.WriteBatch(ctx, "b", []stats.QueryEvent{event(1), event(2)}))
	sum, err := d.Summary(ctx, testStart, testStart.Add(time.Hour))
	require.NoError(t, err)
	assert.Equal(t, uint64(2), sum.Admitted)
}

func TestQueryKeysetFiltersCancellationAndRanking(t *testing.T) {
	d := openTest(t)
	ctx := context.Background()
	for i := uint64(1); i <= 5; i++ {
		e := event(i)
		if i == 5 {
			e.Outcome = stats.FreshCache
		}
		require.NoError(t, d.WriteBatch(ctx, "b", []stats.QueryEvent{e}))
	}
	o := QueryOptions{Start: testStart, End: testStart.Add(time.Hour), Limit: 2, Domain: []byte{1, 'a', 0}}
	p, err := d.Query(ctx, o)
	require.NoError(t, err)
	require.NotNil(t, p.Next)
	assert.Equal(t, uint64(5), p.Rows[0].Event.Sequence)
	require.NoError(t, d.WriteBatch(ctx, "b", []stats.QueryEvent{event(6)}))
	o.Cursor = p.Next
	p, err = d.Query(ctx, o)
	require.NoError(t, err)
	require.Len(t, p.Rows, 2)
	assert.Equal(t, uint64(3), p.Rows[0].Event.Sequence)
	o.Cursor = p.Next
	p, err = d.Query(ctx, o)
	require.NoError(t, err)
	require.Len(t, p.Rows, 1)
	assert.Equal(t, uint64(1), p.Rows[0].Event.Sequence)
	assert.Nil(t, p.Next)
	r, err := d.Rankings(ctx, testStart, testStart.Add(time.Hour))
	require.NoError(t, err)
	require.Len(t, r.Clients, 1)
	assert.Equal(t, uint64(6), r.Clients[0].Count)
	require.Len(t, r.BlockedDomains, 1)
	assert.Equal(t, uint64(5), r.BlockedDomains[0].Count)
	assert.False(t, r.Complete) // Events alone do not observe the rest of the hour.
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = d.Query(canceled, o)
	assert.ErrorIs(t, err, context.Canceled)
	o.Limit = 501
	_, err = d.Query(ctx, o)
	assert.Error(t, err)
	o.Limit = 2
	o.Start = o.End
	_, err = d.Query(ctx, o)
	assert.Error(t, err)
}

func TestRetentionAndNoResurrection(t *testing.T) {
	d := openTest(t)
	ctx := context.Background()
	require.NoError(t, d.WriteBatch(ctx, "b", []stats.QueryEvent{event(1)}))
	now := testStart.Add(8 * 24 * time.Hour)
	require.NoError(t, d.Retain(ctx, now, DefaultRetention()))
	var count int
	for _, table := range []string{"query_events", "domains", "clients"} {
		require.NoError(t, d.write.QueryRow("SELECT COUNT(*) FROM "+table).Scan(&count))
		assert.Zero(t, count, table)
	}
	require.NoError(t, d.WriteBatch(ctx, "b", []stats.QueryEvent{event(1)}))
	require.NoError(t, d.write.QueryRow("SELECT COUNT(*) FROM query_events").Scan(&count))
	assert.Zero(t, count)
	s, err := d.Timeseries(ctx, testStart, testStart.Add(time.Hour), time.Hour)
	require.NoError(t, err)
	assert.Equal(t, uint64(1), s.Points[0].Outcomes[stats.PolicyBlock])
	assert.False(t, s.Complete) // Retention preserves counts, not unobserved coverage.
	m, err := d.Timeseries(ctx, testStart, testStart.Add(time.Minute), time.Minute)
	require.NoError(t, err)
	assert.Zero(t, m.Points[0].Outcomes[stats.PolicyBlock])
	assert.False(t, m.Complete)
	require.NoError(t, d.Retain(ctx, testStart.Add(366*24*time.Hour), DefaultRetention()))
	require.NoError(t, d.write.QueryRow("SELECT COUNT(*) FROM rollups").Scan(&count))
	assert.Zero(t, count)
	cp, err := d.Checkpoint(ctx)
	require.NoError(t, err)
	assert.Positive(t, cp.AllocatedPages)
	assert.Positive(t, cp.PageSize)
}

func TestMigrationRejectAndPragmas(t *testing.T) {
	path := filepath.Join(t.TempDir(), "future")
	raw, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	_, err = raw.Exec("PRAGMA user_version=99")
	require.NoError(t, err)
	require.NoError(t, raw.Close())
	_, err = Open(path)
	require.ErrorContains(t, err, "unsupported storage schema")
	raw, err = sql.Open("sqlite", path)
	require.NoError(t, err)
	_, err = raw.Exec("PRAGMA user_version=1")
	require.NoError(t, err)
	require.NoError(t, raw.Close())
	_, err = Open(path)
	require.ErrorContains(t, err, "invalid storage schema")
	d := openTest(t)
	var version, mode string
	require.NoError(t, d.read.QueryRow("SELECT sqlite_version()").Scan(&version))
	t.Log("SQLite", version)
	require.NoError(t, d.write.QueryRow("PRAGMA journal_mode").Scan(&mode))
	assert.Equal(t, "wal", mode)
	for _, pool := range []*sql.DB{d.read, d.write} {
		var cache, mmap int
		require.NoError(t, pool.QueryRow("PRAGMA cache_size").Scan(&cache))
		assert.Equal(t, -8192, cache)
		require.NoError(t, pool.QueryRow("PRAGMA mmap_size").Scan(&mmap))
		assert.Zero(t, mmap)
	}
	assert.Equal(t, 2, d.read.Stats().MaxOpenConnections)
	assert.Equal(t, 1, d.write.Stats().MaxOpenConnections)
}

func TestWriterBusyAndConsumerOverflow(t *testing.T) {
	d := openTest(t)
	ctx := context.Background()
	tx, err := d.write.BeginTx(ctx, nil)
	require.NoError(t, err)
	c := stats.New(1)
	for range 10000 {
		c.Record(event(0))
	}
	assert.Equal(t, uint64(10000), c.Snapshot().Admitted)
	assert.Equal(t, uint64(9999), c.Snapshot().Dropped)
	short, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	err = d.WriteBatch(short, "b", []stats.QueryEvent{event(1)})
	require.Error(t, err)
	require.NoError(t, tx.Rollback())
	run, cancelRun := context.WithCancel(ctx)
	cancelRun()
	require.NoError(t, d.Run(run, c, "b"))
	s, err := d.Counters(ctx, "b")
	require.NoError(t, err)
	assert.Equal(t, uint64(10000), s.Admitted)
}

func TestWriterFullDiskRollback(t *testing.T) {
	d := openTest(t)
	ctx := context.Background()
	_, err := d.write.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	require.NoError(t, err)
	var pages int
	require.NoError(t, d.write.QueryRow("PRAGMA page_count").Scan(&pages))
	_, err = d.write.Exec("PRAGMA max_page_count=" + strconv.Itoa(pages))
	require.NoError(t, err)
	events := make([]stats.QueryEvent, 512)
	for i := range events {
		events[i] = event(uint64(i + 1))
		events[i].QNameLength = 255
		for j := range events[i].QName {
			events[i].QName[j] = byte(i + j)
		}
		events[i].Client[0] = byte(i)
		events[i].Client[1] = byte(i >> 8)
	}
	err = d.WriteBatch(ctx, "full", events)
	require.ErrorContains(t, err, "full")
	var count int
	require.NoError(t, d.write.QueryRow("SELECT COUNT(*) FROM query_events").Scan(&count))
	assert.Zero(t, count)
	require.NoError(t, d.write.QueryRow("SELECT COUNT(*) FROM rollups").Scan(&count))
	assert.Zero(t, count)
}

func TestCrashRecovery(t *testing.T) {
	if path := os.Getenv("DIMSUM_STORAGE_CRASH_FIXTURE"); path != "" {
		d, err := Open(path)
		if err != nil {
			os.Exit(2)
		}
		if err = d.WriteBatch(context.Background(), "crash", []stats.QueryEvent{event(1)}); err != nil {
			os.Exit(3)
		}
		if os.Getenv("DIMSUM_STORAGE_UNCOMMITTED") == "1" {
			tx, err := d.write.Begin()
			if err != nil {
				os.Exit(4)
			}
			if _, err = tx.Exec("DELETE FROM query_events; UPDATE rollups SET count=99; UPDATE writer_state SET event_watermark=99"); err != nil {
				os.Exit(5)
			}
		}
		// Abrupt exit intentionally skips Close, rollback and passive checkpoint.
		os.Exit(0)
	}
	for _, mode := range []string{"0", "1"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "crash.sqlite")
			exe, err := os.Executable()
			require.NoError(t, err)
			cmd := exec.Command(exe, "-test.run=^TestCrashRecovery$")
			cmd.Env = append(os.Environ(), "DIMSUM_STORAGE_CRASH_FIXTURE="+path, "DIMSUM_STORAGE_UNCOMMITTED="+mode)
			output, err := cmd.CombinedOutput()
			require.NoError(t, err, string(output))
			d, err := Open(path)
			require.NoError(t, err)
			defer d.Close()
			require.NoError(t, d.WriteBatch(context.Background(), "crash", []stats.QueryEvent{event(1)}))
			s, err := d.Summary(context.Background(), testStart, testStart.Add(time.Hour))
			require.NoError(t, err)
			assert.Equal(t, uint64(1), s.Admitted)
			series, err := d.Timeseries(context.Background(), testStart, testStart.Add(time.Hour), time.Hour)
			require.NoError(t, err)
			assert.Equal(t, uint64(1), series.Points[0].Outcomes[stats.PolicyBlock])
		})
	}
}

func TestConsumerFailureReportsReplaySafeLoss(t *testing.T) {
	d := openTest(t)
	_, err := d.write.Exec(`CREATE TRIGGER fail_event BEFORE INSERT ON query_events BEGIN SELECT RAISE(ABORT,'writer fixture'); END`)
	require.NoError(t, err)
	c := stats.New(512)
	for range 512 {
		c.Record(event(0))
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx, c, "failure") }()
	require.Eventually(t, func() bool { return d.Status().LostDetails == 512 }, 3*time.Second, 5*time.Millisecond)
	assert.Contains(t, d.Status().LastError, "writer fixture")
	_, err = d.write.Exec("DROP TRIGGER fail_event")
	require.NoError(t, err)
	c.Record(event(0))
	cancel()
	require.NoError(t, <-done)
	s, err := d.Counters(context.Background(), "failure")
	require.NoError(t, err)
	assert.Equal(t, uint64(513), s.Admitted)
	p, err := d.Query(context.Background(), QueryOptions{Start: testStart, End: testStart.Add(time.Hour)})
	require.NoError(t, err)
	require.Len(t, p.Rows, 1)
	assert.False(t, p.Complete)
	for range 2 {
		require.NoError(t, d.WriteBatch(context.Background(), "failure", nil, BatchOptions{LostDetails: 512}))
	}
	var lost int
	require.NoError(t, d.read.QueryRow("SELECT lost_details FROM writer_state WHERE boot_id='failure'").Scan(&lost))
	assert.Equal(t, 512, lost)
}

func TestReadSnapshotPinsWALAndCancellation(t *testing.T) {
	d := openTest(t)
	ctx := context.Background()
	require.NoError(t, d.WriteBatch(ctx, "wal", []stats.QueryEvent{event(1)}))
	tx, err := d.read.BeginTx(ctx, nil)
	require.NoError(t, err)
	var count int
	require.NoError(t, tx.QueryRow("SELECT COUNT(*) FROM query_events").Scan(&count))
	assert.Equal(t, 1, count)
	require.NoError(t, d.WriteBatch(ctx, "wal", []stats.QueryEvent{event(2)}))
	cp, err := d.Checkpoint(ctx)
	require.NoError(t, err)
	assert.Greater(t, cp.WALPages, cp.CheckpointedPages)
	require.NoError(t, tx.QueryRow("SELECT COUNT(*) FROM query_events").Scan(&count))
	assert.Equal(t, 1, count)
	require.NoError(t, tx.Rollback())
	cp, err = d.Checkpoint(ctx)
	require.NoError(t, err)
	assert.Equal(t, cp.WALPages, cp.CheckpointedPages)
	// Exercise actual in-flight SQLite cancellation, not only a pre-canceled call.
	short, cancel := context.WithTimeout(ctx, 5*time.Millisecond)
	defer cancel()
	err = d.read.QueryRowContext(short, `WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<1000000000) SELECT SUM(x) FROM n`).Scan(&count)
	require.Error(t, err)
	require.NoError(t, d.read.QueryRow("SELECT COUNT(*) FROM query_events").Scan(&count))
	assert.Equal(t, 2, count)
}
