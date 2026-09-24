package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type Retention struct{ Detail, Minute, Hour, Day time.Duration }

func DefaultRetention() Retention {
	return Retention{7 * 24 * time.Hour, 7 * 24 * time.Hour, 90 * 24 * time.Hour, 365 * 24 * time.Hour}
}

// SetRetention applies authoritative configuration to subsequent maintenance
// without restarting the consumer. Expanding retention cannot restore old rows.
func (d *DB) SetRetention(r Retention) error {
	for _, v := range []time.Duration{r.Detail, r.Minute, r.Hour, r.Day} {
		if v <= 0 || v > 3650*24*time.Hour {
			return errors.New("retention must be positive and at most 3650 days")
		}
	}
	d.statusMu.Lock()
	d.retention = r
	d.statusMu.Unlock()
	return nil
}

func (d *DB) Retention() Retention { d.statusMu.Lock(); defer d.statusMu.Unlock(); return d.retention }

// Retain performs one short transaction, deleting at most 512 expired rows per
// table/resolution, checking their at-most-512 reference identities per orphan
// table, and examining 128 additional rows per orphan table. Call repeatedly
// to finish the persisted sweep. Cutoffs mark
// data unavailable as soon as expiry is requested; physical cleanup is incremental.
func (d *DB) Retain(ctx context.Context, now time.Time, r Retention) (retErr error) {
	defer func() {
		if retErr != nil {
			d.statusMu.Lock()
			d.status.Backlogged = true
			d.statusMu.Unlock()
		}
	}()
	for _, v := range []time.Duration{r.Detail, r.Minute, r.Hour, r.Day} {
		if v <= 0 || v > 3650*24*time.Hour {
			return errors.New("retention must be positive and at most 3650 days")
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	tx, err := d.write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, v := range []struct {
		key      string
		duration time.Duration
	}{{"detail_cutoff", r.Detail}, {"minute_cutoff", r.Minute}, {"hour_cutoff", r.Hour}, {"day_cutoff", r.Day}} {
		if _, err = tx.ExecContext(ctx, "UPDATE storage_meta SET value=MAX(value,?) WHERE key=?", now.Add(-v.duration).UnixMicro(), v.key); err != nil {
			return err
		}
	}
	if err := expireDetails(ctx, tx); err != nil {
		return err
	}
	for _, v := range []struct {
		resolution int
		key        string
	}{{60, "minute_cutoff"}, {3600, "hour_cutoff"}, {86400, "day_cutoff"}} {
		// Only remove fully expired buckets.
		if _, err = tx.ExecContext(ctx, "DELETE FROM rollups WHERE rowid IN (SELECT rowid FROM rollups WHERE resolution=? AND bucket <= (SELECT value-? FROM storage_meta WHERE key=?) ORDER BY bucket LIMIT 512)", v.resolution, int64(v.resolution)*1000000, v.key); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM rankings_hour WHERE rowid IN (SELECT rowid FROM rankings_hour WHERE bucket<=(SELECT value-3600000000 FROM storage_meta WHERE key='hour_cutoff') ORDER BY bucket LIMIT 512)"); err != nil {
		return err
	}
	var pending bool
	if err = tx.QueryRowContext(ctx, expiredBacklogSQL).Scan(&pending); err != nil {
		return err
	}
	for _, v := range []struct{ table, reference string }{
		{"domains", "e.domain_id=d.id"},
		{"clients", "e.client_id=d.id"},
		{"rule_versions", "e.boot_id=d.boot_id AND e.generation=d.generation AND e.rule_id=d.rule_id"},
	} {
		more, err := sweepOrphans(ctx, tx, v.table, v.reference)
		if err != nil {
			return err
		}
		pending = pending || more
	}
	if !pending {
		// Next scheduled maintenance starts a fresh finite sweep. Failed calls
		// roll back both the deletion work and these cursor updates together.
		if _, err = tx.ExecContext(ctx, "DELETE FROM storage_meta WHERE key IN ('sweep_domains_cursor','sweep_domains_end','sweep_clients_cursor','sweep_clients_end','sweep_rule_versions_cursor','sweep_rule_versions_end','sweep_rule_versions_revisit')"); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, "UPDATE storage_meta SET value=? WHERE key='retention_pending'", pending); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	d.statusMu.Lock()
	d.status.Backlogged = pending
	d.statusMu.Unlock()
	return nil
}

// Indexed existence checks report remaining work, rather than treating a full
// deletion batch as proof of either completion or backlog.
const expiredBacklogSQL = `SELECT
 EXISTS(SELECT 1 FROM query_events WHERE timestamp<(SELECT value FROM storage_meta WHERE key='detail_cutoff'))
 OR EXISTS(SELECT 1 FROM rollups WHERE resolution=60 AND bucket<=(SELECT value-60000000 FROM storage_meta WHERE key='minute_cutoff'))
 OR EXISTS(SELECT 1 FROM rollups WHERE resolution=3600 AND bucket<=(SELECT value-3600000000 FROM storage_meta WHERE key='hour_cutoff'))
 OR EXISTS(SELECT 1 FROM rollups WHERE resolution=86400 AND bucket<=(SELECT value-86400000000 FROM storage_meta WHERE key='day_cutoff'))
 OR EXISTS(SELECT 1 FROM rankings_hour WHERE bucket<=(SELECT value-3600000000 FROM storage_meta WHERE key='hour_cutoff'))`

// Legacy migration only. Runtime maintenance uses persisted bounded sweeps,
// rather than rescanning every live dictionary row merely to report backlog.
const retentionBacklogSQL = expiredBacklogSQL + `
 OR EXISTS(SELECT 1 FROM domains d WHERE NOT EXISTS(SELECT 1 FROM query_events e WHERE e.domain_id=d.id))
 OR EXISTS(SELECT 1 FROM clients c WHERE NOT EXISTS(SELECT 1 FROM query_events e WHERE e.client_id=c.id))
 OR EXISTS(SELECT 1 FROM rule_versions r WHERE NOT EXISTS(SELECT 1 FROM query_events e WHERE e.boot_id=r.boot_id AND e.generation=r.generation AND e.rule_id=r.rule_id))`

func sweepOrphans(ctx context.Context, tx *sql.Tx, table, reference string) (bool, error) {
	cursorKey, endKey := "sweep_"+table+"_cursor", "sweep_"+table+"_end"
	var cursor, end int64
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE((SELECT value FROM storage_meta WHERE key=?),-1),COALESCE((SELECT value FROM storage_meta WHERE key=?),-1)", cursorKey, endKey).Scan(&cursor, &end); err != nil {
		return false, err
	}
	if cursor < 0 || end < 0 {
		cursor = 0
		if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(rowid),0) FROM "+table).Scan(&end); err != nil {
			return false, err
		}
	}
	if cursor < end {
		var next int64
		if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(rowid),0) FROM (SELECT rowid FROM "+table+" WHERE rowid>? AND rowid<=? ORDER BY rowid LIMIT 128)", cursor, end).Scan(&next); err != nil {
			return false, err
		}
		if next == 0 {
			next = end
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" AS d WHERE rowid>? AND rowid<=? AND NOT EXISTS(SELECT 1 FROM query_events e WHERE "+reference+")", cursor, next); err != nil {
			return false, err
		}
		cursor = next
	}
	if table == "rule_versions" && cursor >= end {
		var revisit int64
		if err := tx.QueryRowContext(ctx, "SELECT COALESCE((SELECT value FROM storage_meta WHERE key='sweep_rule_versions_revisit'),-1)").Scan(&revisit); err != nil {
			return false, err
		}
		if revisit >= 0 {
			// Finish each finite pass before capturing a new endpoint. Neither
			// appends nor reused IDs may move the active pass's finishing line.
			cursor = min(cursor, revisit)
			if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(rowid),0) FROM rule_versions").Scan(&end); err != nil {
				return false, err
			}
			cursor = min(cursor, end)
			if _, err := tx.ExecContext(ctx, "DELETE FROM storage_meta WHERE key='sweep_rule_versions_revisit'"); err != nil {
				return false, err
			}
		}
	}
	_, err := tx.ExecContext(ctx, "INSERT INTO storage_meta(key,value) VALUES(?,?),(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", cursorKey, cursor, endKey, end)
	return cursor < end, err
}

// Expiration knows exactly which bounded set of references can become orphaned.
// Clean those directly rather than restarting a sweep during continuous expiry.
func expireDetails(ctx context.Context, tx *sql.Tx) error {
	var pending bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM query_events WHERE timestamp<(SELECT value FROM storage_meta WHERE key='detail_cutoff'))").Scan(&pending); err != nil {
		return err
	}
	if !pending {
		return nil
	}
	for _, query := range []string{
		`CREATE TEMP TABLE IF NOT EXISTS retention_candidates(event_id INTEGER PRIMARY KEY,domain_id INTEGER,client_id INTEGER,boot_id TEXT,generation INTEGER,rule_id INTEGER)`,
		`DELETE FROM retention_candidates`,
		`INSERT INTO retention_candidates SELECT id,domain_id,client_id,boot_id,generation,rule_id FROM query_events WHERE timestamp<(SELECT value FROM storage_meta WHERE key='detail_cutoff') ORDER BY timestamp,id LIMIT 512`,
		`DELETE FROM query_events WHERE id IN (SELECT event_id FROM retention_candidates)`,
		`DELETE FROM domains AS d WHERE id IN (SELECT domain_id FROM retention_candidates) AND NOT EXISTS(SELECT 1 FROM query_events e WHERE e.domain_id=d.id)`,
		`DELETE FROM clients AS d WHERE id IN (SELECT client_id FROM retention_candidates) AND NOT EXISTS(SELECT 1 FROM query_events e WHERE e.client_id=d.id)`,
		`DELETE FROM rule_versions AS d WHERE (boot_id,generation,rule_id) IN (SELECT boot_id,generation,rule_id FROM retention_candidates) AND NOT EXISTS(SELECT 1 FROM query_events e WHERE e.boot_id=d.boot_id AND e.generation=d.generation AND e.rule_id=d.rule_id)`,
		`DELETE FROM retention_candidates`,
	} {
		if _, err := tx.ExecContext(ctx, query); err != nil {
			return err
		}
	}
	return nil
}

func noteRuleForSweep(ctx context.Context, tx *sql.Tx, rowid int64) error {
	var cursor, end int64
	err := tx.QueryRowContext(ctx, "SELECT c.value,e.value FROM storage_meta c JOIN storage_meta e ON e.key='sweep_rule_versions_end' WHERE c.key='sweep_rule_versions_cursor'").Scan(&cursor, &end)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if rowid <= cursor || rowid > end {
		_, err = tx.ExecContext(ctx, "INSERT INTO storage_meta(key,value) VALUES('sweep_rule_versions_revisit',?) ON CONFLICT(key) DO UPDATE SET value=MIN(value,excluded.value)", rowid-1)
	}
	return err
}

type Checkpoint struct {
	Busy, WALPages, CheckpointedPages   int
	AllocatedPages, FreePages, PageSize int64
}

func (d *DB) Checkpoint(ctx context.Context) (Checkpoint, error) {
	var r Checkpoint
	d.mu.Lock()
	defer d.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	err := d.write.QueryRowContext(ctx, "PRAGMA wal_checkpoint(PASSIVE)").Scan(&r.Busy, &r.WALPages, &r.CheckpointedPages)
	if err != nil {
		return r, err
	}
	for _, v := range []struct {
		q string
		p *int64
	}{{"PRAGMA page_count", &r.AllocatedPages}, {"PRAGMA freelist_count", &r.FreePages}, {"PRAGMA page_size", &r.PageSize}} {
		if err = d.write.QueryRowContext(ctx, v.q).Scan(v.p); err != nil {
			return r, err
		}
	}
	return r, nil
}
