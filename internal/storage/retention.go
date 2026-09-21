package storage

import (
	"context"
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
		if v <= 0 || v > 366*24*time.Hour {
			return errors.New("retention must be between zero and 366 days")
		}
	}
	d.statusMu.Lock()
	d.retention = r
	d.statusMu.Unlock()
	return nil
}

func (d *DB) Retention() Retention { d.statusMu.Lock(); defer d.statusMu.Unlock(); return d.retention }

// Retain performs one short transaction, deleting at most 512 rows per table or
// resolution and 512 orphan dimensions. Call repeatedly to catch up. Cutoffs mark
// data unavailable as soon as expiry is requested; physical cleanup is incremental.
func (d *DB) Retain(ctx context.Context, now time.Time, r Retention) error {
	for _, v := range []time.Duration{r.Detail, r.Minute, r.Hour, r.Day} {
		if v <= 0 || v > 366*24*time.Hour {
			return errors.New("retention must be between zero and 366 days")
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
	if _, err = tx.ExecContext(ctx, "DELETE FROM query_events WHERE id IN (SELECT id FROM query_events WHERE timestamp<(SELECT value FROM storage_meta WHERE key='detail_cutoff') ORDER BY timestamp,id LIMIT 512)"); err != nil {
		return err
	}
	for _, v := range []struct {
		resolution int
		key        string
	}{{60, "minute_cutoff"}, {3600, "hour_cutoff"}, {86400, "day_cutoff"}} {
		// Only remove fully expired buckets.
		if _, err = tx.ExecContext(ctx, "DELETE FROM rollups WHERE rowid IN (SELECT rowid FROM rollups WHERE resolution=? AND bucket+? <= (SELECT value FROM storage_meta WHERE key=?) ORDER BY bucket LIMIT 512)", v.resolution, int64(v.resolution)*1000000, v.key); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM rankings_hour WHERE rowid IN (SELECT rowid FROM rankings_hour WHERE bucket+3600000000<=(SELECT value FROM storage_meta WHERE key='hour_cutoff') ORDER BY bucket LIMIT 512)"); err != nil {
		return err
	}
	for _, v := range []struct{ table, column string }{{"domains", "domain_id"}, {"clients", "client_id"}} {
		if _, err = tx.ExecContext(ctx, "DELETE FROM "+v.table+" WHERE id IN (SELECT id FROM "+v.table+" d WHERE NOT EXISTS(SELECT 1 FROM query_events e WHERE e."+v.column+"=d.id) LIMIT 512)"); err != nil {
			return err
		}
	}
	// Supply explanations with event batches; unreferenced preregistration may
	// be removed by maintenance. Referenced historical versions remain immutable.
	if _, err = tx.ExecContext(ctx, "DELETE FROM rule_versions WHERE rowid IN (SELECT rowid FROM rule_versions r WHERE NOT EXISTS(SELECT 1 FROM query_events e WHERE e.generation=r.generation AND e.rule_id=r.rule_id) LIMIT 512)"); err != nil {
		return err
	}
	return tx.Commit()
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
