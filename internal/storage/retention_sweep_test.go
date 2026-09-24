package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func seedLiveRetentionDomains(t *testing.T, d *DB, count int) {
	t.Helper()
	_, err := d.write.Exec(`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<?) INSERT INTO domains(id,name) SELECT i,CAST(printf('synthetic%d.example',i) AS BLOB) FROM n`, count)
	require.NoError(t, err)
	_, err = d.write.Exec(`INSERT INTO clients(id,address) VALUES(1,zeroblob(16))`)
	require.NoError(t, err)
	_, err = d.write.Exec(`INSERT INTO query_events(boot_id,sequence,timestamp,duration,domain_id,client_id,qtype,qclass,outcome,rcode,upstream_id,generation,rule_id,flags) SELECT 'sweep',id,?,10,id,1,1,1,1,0,0,0,0,0 FROM domains`, testStart.UnixMicro())
	require.NoError(t, err)
}

func TestRetentionBoundsLiveScanAndResumesAfterReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.sqlite")
	d, err := Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, d.Close()) })
	seedLiveRetentionDomains(t, d, 2048)
	_, err = d.write.Exec(`INSERT INTO domains(id,name) VALUES(3000,x'017800')`)
	require.NoError(t, err)
	ctx := context.Background()
	require.NoError(t, d.Retain(ctx, testStart, DefaultRetention()))
	assert.True(t, d.Status().Backlogged, "an unfinished bounded sweep must remain pending")
	var count int
	require.NoError(t, d.read.QueryRow("SELECT COUNT(*) FROM domains WHERE id=3000").Scan(&count))
	assert.Equal(t, 1, count, "maintenance must not scan all live entries to reach a distant orphan in one transaction")
	require.NoError(t, d.Close())
	d, err = Open(path)
	require.NoError(t, err)
	assert.True(t, d.Status().Backlogged)
	for i := 0; i < 40 && d.Status().Backlogged; i++ {
		require.NoError(t, d.Retain(ctx, testStart, DefaultRetention()))
	}
	assert.False(t, d.Status().Backlogged)
	require.NoError(t, d.read.QueryRow("SELECT COUNT(*) FROM domains").Scan(&count))
	assert.Equal(t, 2048, count, "only the orphan may be deleted")
	require.NoError(t, d.read.QueryRow("SELECT COUNT(*) FROM query_events").Scan(&count))
	assert.Equal(t, 2048, count)
}

func TestRetentionRevisitsOrphansCreatedBehindSweep(t *testing.T) {
	d := openTest(t)
	seedLiveRetentionDomains(t, d, 1024)
	ctx := context.Background()
	require.NoError(t, d.Retain(ctx, testStart, DefaultRetention()))
	// Expiring a previously scanned domain must invalidate the earlier sweep.
	_, err := d.write.Exec("UPDATE query_events SET timestamp=? WHERE domain_id=1", testStart.Add(-10*24*time.Hour).UnixMicro())
	require.NoError(t, err)
	for range 20 {
		require.NoError(t, d.Retain(ctx, testStart, DefaultRetention()))
		if !d.Status().Backlogged {
			break
		}
	}
	assert.False(t, d.Status().Backlogged)
	var count int
	require.NoError(t, d.read.QueryRow("SELECT COUNT(*) FROM domains WHERE id=1").Scan(&count))
	assert.Zero(t, count)
}

func TestRetentionRollbackPreservesSweepAndOrphans(t *testing.T) {
	d := openTest(t)
	seedLiveRetentionDomains(t, d, 512)
	ctx := context.Background()
	require.NoError(t, d.Retain(ctx, testStart, DefaultRetention()))
	var cursor int64
	require.NoError(t, d.read.QueryRow("SELECT value FROM storage_meta WHERE key='sweep_domains_cursor'").Scan(&cursor))
	// Make the next row an orphan, then fail after domain deletion and cursor
	// update, while the following table's progress is being persisted.
	_, err := d.write.Exec("DELETE FROM query_events WHERE domain_id=?", cursor+1)
	require.NoError(t, err)
	_, err = d.write.Exec(`CREATE TRIGGER fail_sweep BEFORE INSERT ON storage_meta WHEN NEW.key='sweep_clients_cursor' BEGIN SELECT RAISE(ABORT,'injected sweep failure'); END`)
	require.NoError(t, err)
	require.ErrorContains(t, d.Retain(ctx, testStart, DefaultRetention()), "injected sweep failure")
	var saved int64
	require.NoError(t, d.read.QueryRow("SELECT value FROM storage_meta WHERE key='sweep_domains_cursor'").Scan(&saved))
	assert.Equal(t, cursor, saved, "failed deletion and progress must roll back together")
	var count int
	require.NoError(t, d.read.QueryRow("SELECT COUNT(*) FROM domains WHERE id=?", cursor+1).Scan(&count))
	assert.Equal(t, 1, count)
	_, err = d.write.Exec("DROP TRIGGER fail_sweep")
	require.NoError(t, err)
	require.NoError(t, d.Retain(ctx, testStart, DefaultRetention()))
	require.NoError(t, d.read.QueryRow("SELECT COUNT(*) FROM domains WHERE id=?", cursor+1).Scan(&count))
	assert.Zero(t, count)
}

func TestRulePreregistrationInvalidatesFinishedSweep(t *testing.T) {
	d := openTest(t)
	seedLiveRetentionDomains(t, d, 512)
	ctx := context.Background()
	require.NoError(t, d.WriteBatch(ctx, "rules", nil, BatchOptions{Rules: []RuleVersion{{Generation: 1, RuleID: 1, Description: "first"}}}))
	require.NoError(t, d.Retain(ctx, testStart, DefaultRetention()))
	assert.True(t, d.Status().Backlogged, "domain sweep remains active after the small rule sweep finishes")
	var count int
	require.NoError(t, d.read.QueryRow("SELECT COUNT(*) FROM rule_versions").Scan(&count))
	assert.Zero(t, count)
	// SQLite reuses rowid=1 here, behind the completed rule sweep's cursor.
	require.NoError(t, d.WriteBatch(ctx, "rules", nil, BatchOptions{Rules: []RuleVersion{{Generation: 1, RuleID: 2, Description: "second"}}}))
	require.NoError(t, d.Retain(ctx, testStart, DefaultRetention()))
	assert.True(t, d.Status().Backlogged, "reused rowids require a pending revisit")
	require.NoError(t, d.Retain(ctx, testStart, DefaultRetention()))
	require.NoError(t, d.read.QueryRow("SELECT COUNT(*) FROM rule_versions").Scan(&count))
	assert.Zero(t, count, "new preregistration must not be skipped behind a saved cursor")
}

func TestRetentionProgressDuringContinuousExpiry(t *testing.T) {
	d := openTest(t)
	seedLiveRetentionDomains(t, d, 2048)
	ctx := context.Background()
	_, err := d.write.Exec(`INSERT INTO domains(id,name) VALUES(3000,x'017800')`)
	require.NoError(t, err)
	for i := 0; i < 30; i++ {
		_, err = d.write.Exec("UPDATE query_events SET timestamp=? WHERE domain_id=?", testStart.Add(-10*24*time.Hour).UnixMicro(), 2048-i)
		require.NoError(t, err)
		require.NoError(t, d.Retain(ctx, testStart, DefaultRetention()))
	}
	var count int
	require.NoError(t, d.read.QueryRow("SELECT COUNT(*) FROM domains WHERE id=3000").Scan(&count))
	assert.Zero(t, count, "new expiry must not prevent reaching an existing distant orphan")
}

func TestRuleSweepProgressDuringPreregistration(t *testing.T) {
	d := openTest(t)
	seedLiveRetentionDomains(t, d, 1024)
	ctx := context.Background()
	_, err := d.write.Exec(`INSERT INTO rule_versions(rowid,boot_id,generation,rule_id,description,source_id) SELECT id,'sweep',0,id,'referenced','' FROM domains;
UPDATE query_events SET rule_id=domain_id;
INSERT INTO rule_versions(rowid,boot_id,generation,rule_id,description,source_id) VALUES(3000,'orphan',0,1,'unreferenced','');`)
	require.NoError(t, err)
	for i := 0; i < 20; i++ {
		require.NoError(t, d.WriteBatch(ctx, "newrules", nil, BatchOptions{Rules: []RuleVersion{{Generation: 1, RuleID: uint32(i + 1), Description: "new"}}}))
		require.NoError(t, d.Retain(ctx, testStart, DefaultRetention()))
	}
	var count int
	require.NoError(t, d.read.QueryRow("SELECT COUNT(*) FROM rule_versions WHERE boot_id='orphan'").Scan(&count))
	assert.Zero(t, count, "new rules must not keep restarting a sweep of earlier rows")
	require.NoError(t, d.read.QueryRow("SELECT COUNT(*) FROM rule_versions WHERE boot_id='sweep'").Scan(&count))
	assert.Equal(t, 1024, count)
}

func TestRuleRevisitProgressWhileNewRulesAppend(t *testing.T) {
	d := openTest(t)
	seedLiveRetentionDomains(t, d, 4096)
	ctx := context.Background()
	var id uint32
	register := func(count int) {
		rules := make([]RuleVersion, count)
		for i := range rules {
			id++
			rules[i] = RuleVersion{Generation: 1, RuleID: id, Description: "unreferenced"}
		}
		require.NoError(t, d.WriteBatch(ctx, "append", nil, BatchOptions{Rules: rules}))
	}
	register(128)
	require.NoError(t, d.Retain(ctx, testStart, DefaultRetention()))
	// The first sweep deletes every rule. SQLite now reuses rowids 1..512,
	// putting the first 128 new rules behind the saved cursor.
	register(512)
	require.NoError(t, d.Retain(ctx, testStart, DefaultRetention()))
	for range 10 {
		register(128)
		require.NoError(t, d.Retain(ctx, testStart, DefaultRetention()))
	}
	var remaining int
	require.NoError(t, d.read.QueryRow("SELECT COUNT(*) FROM rule_versions WHERE boot_id='append' AND rule_id BETWEEN 129 AND 256").Scan(&remaining))
	assert.Zero(t, remaining, "appends must not indefinitely postpone a behind-cursor revisit")
}
