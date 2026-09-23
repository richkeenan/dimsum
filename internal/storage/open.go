// Package storage persists disposable query history independently of DNS workers.
package storage

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/001_initial.sql
var initialSchema string

//go:embed migrations/002_boot_coverage.sql
var bootCoverageSchema string

//go:embed migrations/003_rule_source.sql
var ruleSourceSchema string

//go:embed migrations/004_latency.sql
var latencySchema string

//go:embed migrations/005_query_response.sql
var queryResponseSchema string

//go:embed migrations/006_client_names.sql
var clientNamesSchema string

type DB struct {
	write     *sql.DB
	read      *sql.DB
	mu        sync.Mutex
	statusMu  sync.Mutex
	status    Status
	retention Retention
	running   bool
}

type Status struct {
	LastError   string
	LostDetails uint64
	LastSuccess time.Time
	Backlogged  bool
}

func (d *DB) Status() Status { d.statusMu.Lock(); defer d.statusMu.Unlock(); return d.status }

// Open opens a file-backed store. Each connection disables mmap and has an 8 MiB
// maximum page cache (24 MiB across one writer and two reader connections).
// Normal startup performs schema validation only, without bulk heap restoration.
// A v1 upgrade additionally attributes retained legacy rules inside SQLite, in a
// bounded migration transaction; a failed migration leaves the prior version.
func Open(path string) (*DB, error) {
	if path == "" || path == ":memory:" {
		return nil, errors.New("storage requires a file path")
	}
	p, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: p}
	q := url.Values{}
	for _, v := range []string{"busy_timeout(1000)", "cache_size(-8192)", "mmap_size(0)", "foreign_keys(1)", "synchronous(NORMAL)"} {
		q.Add("_pragma", v)
	}
	u.RawQuery = q.Encode()
	w, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	w.SetMaxOpenConns(1)
	w.SetMaxIdleConns(1)
	d := &DB{write: w, retention: DefaultRetention()}
	fail := func(err error) (*DB, error) { w.Close(); return nil, err }
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var version string
	if err = w.QueryRowContext(ctx, "SELECT sqlite_version()").Scan(&version); err != nil {
		return fail(err)
	}
	parts := strings.Split(version, ".")
	var nums [3]int
	for i := 0; i < 3 && i < len(parts); i++ {
		nums[i], _ = strconv.Atoi(parts[i])
	}
	if nums[0] < 3 || (nums[0] == 3 && (nums[1] < 51 || (nums[1] == 51 && nums[2] < 3))) {
		return fail(fmt.Errorf("SQLite %s lacks required WAL fix", version))
	}
	var mode string
	if err = w.QueryRowContext(ctx, "PRAGMA journal_mode=WAL").Scan(&mode); err != nil {
		return fail(err)
	}
	if mode != "wal" {
		return fail(fmt.Errorf("unexpected journal mode %s", mode))
	}
	var schema int
	if err = w.QueryRowContext(ctx, "PRAGMA user_version").Scan(&schema); err != nil {
		return fail(err)
	}
	if schema > 6 {
		return fail(fmt.Errorf("unsupported storage schema %d", schema))
	}
	if schema == 0 {
		tx, e := w.BeginTx(ctx, nil)
		if e != nil {
			return fail(e)
		}
		if _, e = tx.ExecContext(ctx, initialSchema); e != nil {
			tx.Rollback()
			return fail(e)
		}
		if e = tx.Commit(); e != nil {
			return fail(e)
		}
	}
	if schema < 2 {
		tx, e := w.BeginTx(ctx, nil)
		if e != nil {
			return fail(e)
		}
		if _, e = tx.ExecContext(ctx, bootCoverageSchema); e != nil {
			tx.Rollback()
			return fail(fmt.Errorf("invalid storage schema migration: %w", e))
		}
		var pending bool
		if e = tx.QueryRowContext(ctx, retentionBacklogSQL).Scan(&pending); e != nil {
			tx.Rollback()
			return fail(e)
		}
		if _, e = tx.ExecContext(ctx, "UPDATE storage_meta SET value=? WHERE key='retention_pending'", pending); e != nil {
			tx.Rollback()
			return fail(e)
		}
		if e = tx.Commit(); e != nil {
			return fail(e)
		}
	}
	if schema < 3 {
		tx, e := w.BeginTx(ctx, nil)
		if e != nil {
			return fail(e)
		}
		if _, e = tx.ExecContext(ctx, ruleSourceSchema); e != nil {
			tx.Rollback()
			return fail(fmt.Errorf("invalid storage schema migration: %w", e))
		}
		if e = tx.Commit(); e != nil {
			return fail(e)
		}
	}
	if schema < 4 {
		tx, e := w.BeginTx(ctx, nil)
		if e != nil {
			return fail(e)
		}
		if _, e = tx.ExecContext(ctx, latencySchema); e != nil {
			tx.Rollback()
			return fail(fmt.Errorf("invalid storage schema migration: %w", e))
		}
		if e = tx.Commit(); e != nil {
			return fail(e)
		}
	}
	if schema < 5 {
		tx, e := w.BeginTx(ctx, nil)
		if e != nil {
			return fail(e)
		}
		if _, e = tx.ExecContext(ctx, queryResponseSchema); e != nil {
			tx.Rollback()
			return fail(fmt.Errorf("invalid storage schema migration: %w", e))
		}
		if e = tx.Commit(); e != nil {
			return fail(e)
		}
	}
	if schema < 6 {
		tx, e := w.BeginTx(ctx, nil)
		if e != nil {
			return fail(e)
		}
		if _, e = tx.ExecContext(ctx, clientNamesSchema); e != nil {
			tx.Rollback()
			return fail(fmt.Errorf("invalid storage schema migration: %w", e))
		}
		if e = tx.Commit(); e != nil {
			return fail(e)
		}
	}
	// Validate the versioned schema without scanning retained history. A claimed
	// version with missing/incompatible tables must fail startup, not the first DNS
	// consumer flush.
	check, err := w.PrepareContext(ctx, `SELECT e.sequence,e.alias,e.response,d.name,c.address,r.boot_id,r.description,r.source_id,u.h7,k.count,s.snapshot_watermark,s.snapshot_sequence,s.coverage_start,s.coverage_end,s.lost_details,m.value,l.bin,l.count FROM query_events e,domains d,clients c,rule_versions r,rollups u,rankings_hour k,writer_state s,storage_meta m,latency_bins l WHERE 0`)
	if err != nil {
		return fail(fmt.Errorf("invalid storage schema: %w", err))
	}
	check.Close()
	check, err = w.PrepareContext(ctx, `SELECT kind,address,scope,payload FROM client_names WHERE 0`)
	if err != nil {
		return fail(fmt.Errorf("invalid storage schema: %w", err))
	}
	check.Close()
	if err = w.QueryRowContext(ctx, "SELECT value FROM storage_meta WHERE key='retention_pending'").Scan(&d.status.Backlogged); err != nil {
		return fail(err)
	}
	q.Add("_pragma", "query_only(1)")
	u.RawQuery = q.Encode()
	d.read, err = sql.Open("sqlite", u.String())
	if err != nil {
		return fail(err)
	}
	d.read.SetMaxOpenConns(2)
	d.read.SetMaxIdleConns(2)
	if err = d.read.PingContext(ctx); err != nil {
		d.read.Close()
		return fail(err)
	}
	return d, nil
}

func (d *DB) Close() error { return errors.Join(d.read.Close(), d.write.Close()) }
