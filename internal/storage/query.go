package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/richkeenan/dimsum/internal/stats"
)

type Cursor struct {
	Timestamp int64
	ID        int64
	Ceiling   int64
}
type QueryOptions struct {
	HistoryFilters
	Start, End time.Time // Half-open UTC instants; maximum 366 days.
	Limit      int       // Default 100, maximum 500.
	Cursor     *Cursor
	Domain     []byte // Exact canonical wire name, not a suffix or presentation name.
	Client     []byte // Exactly 16 normalized address bytes.
	Outcome    *stats.Outcome
	QType      *uint16
}
type Row struct {
	ID              int64
	Boot            string
	Event           stats.QueryEvent
	Alias           []byte
	RuleDescription string
	SourceID        string
}
type Page struct {
	Rows     []Row
	Next     *Cursor
	Complete bool
}

func validateWindow(start, end time.Time) error {
	if !end.After(start) || end.Sub(start) > 366*24*time.Hour {
		return errors.New("window must be positive and at most 366 days")
	}
	return nil
}

// Query uses descending (timestamp,id) keyset pagination. The first page fixes an
// insertion ceiling so concurrent inserts, even backdated ones, cannot enter later
// pages. Cursors are only valid with the original filters and window.
func (d *DB) Query(ctx context.Context, o QueryOptions) (Page, error) {
	p := Page{Rows: []Row{}}
	if err := o.HistoryFilters.Validate(); err != nil {
		return p, err
	}
	if err := validateWindow(o.Start, o.End); err != nil {
		return p, err
	}
	if o.Limit == 0 {
		o.Limit = 100
	}
	if o.Limit < 1 || o.Limit > 500 || len(o.Domain) > 255 || (len(o.Client) != 0 && len(o.Client) != 16) {
		return p, errors.New("invalid query bounds")
	}
	if o.Outcome != nil && *o.Outcome >= stats.OutcomeCount {
		return p, errors.New("invalid outcome")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	view, err := d.read.BeginTx(ctx, nil)
	if err != nil {
		return p, err
	}
	defer view.Rollback()
	var ceiling int64
	if o.Cursor == nil {
		if err := view.QueryRowContext(ctx, "SELECT COALESCE(MAX(id),0) FROM query_events").Scan(&ceiling); err != nil {
			return p, err
		}
	} else {
		ceiling = o.Cursor.Ceiling
		if ceiling < 0 || o.Cursor.ID <= 0 || o.Cursor.ID > ceiling {
			return p, errors.New("invalid cursor")
		}
	}
	query := `SELECT e.id,e.boot_id,e.sequence,e.timestamp,e.duration,e.generation,c.address,n.name,e.qtype,e.qclass,e.outcome,e.rcode,e.upstream_id,e.rule_id,e.flags,e.alias,COALESCE(r.description,''),COALESCE(r.source_id,'') FROM query_events e JOIN domains n ON n.id=e.domain_id JOIN clients c ON c.id=e.client_id LEFT JOIN rule_versions r ON r.boot_id=e.boot_id AND r.generation=e.generation AND r.rule_id=e.rule_id WHERE e.timestamp>=? AND e.timestamp<? AND e.id<=?`
	query += " AND e.timestamp >= (SELECT value FROM storage_meta WHERE key='detail_cutoff')"
	args := []any{o.Start.UnixMicro(), o.End.UnixMicro(), ceiling}
	clause, filterArgs := o.HistoryFilters.sql()
	query += clause
	args = append(args, filterArgs...)
	if len(o.Domain) > 0 {
		query += " AND e.domain_id=(SELECT id FROM domains WHERE name=?)"
		args = append(args, o.Domain)
	}
	if len(o.Client) > 0 {
		query += " AND e.client_id=(SELECT id FROM clients WHERE address=?)"
		args = append(args, o.Client)
	}
	if o.Outcome != nil {
		query += " AND e.outcome=?"
		args = append(args, *o.Outcome)
	}
	if o.QType != nil {
		query += " AND e.qtype=?"
		args = append(args, *o.QType)
	}
	if o.Cursor != nil {
		query += " AND (e.timestamp,e.id)<(?,?)"
		args = append(args, o.Cursor.Timestamp, o.Cursor.ID)
	}
	query += " ORDER BY e.timestamp DESC,e.id DESC LIMIT ?"
	args = append(args, o.Limit+1)
	rows, err := view.QueryContext(ctx, query, args...)
	if err != nil {
		return p, err
	}
	for rows.Next() {
		var r Row
		var client, name []byte
		e := &r.Event
		if err = rows.Scan(&r.ID, &r.Boot, &e.Sequence, &e.Timestamp, &e.Duration, &e.Generation, &client, &name, &e.QType, &e.QClass, &e.Outcome, &e.RCode, &e.UpstreamID, &e.RuleID, &e.Flags, &r.Alias, &r.RuleDescription, &r.SourceID); err != nil {
			rows.Close()
			return p, err
		}
		copy(e.Client[:], client)
		copy(e.QName[:], name)
		e.QNameLength = uint8(len(name))
		p.Rows = append(p.Rows, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return p, err
	}
	if len(p.Rows) > o.Limit {
		p.Rows = p.Rows[:o.Limit]
		last := p.Rows[len(p.Rows)-1]
		p.Next = &Cursor{last.Event.Timestamp, last.ID, ceiling}
	}
	p.Complete, err = complete(ctx, view, o.Start, o.End, "detail_cutoff")
	return p, err
}

// Completeness is deliberately conservative: any recorded process/detail loss
// makes retained event-derived views incomplete, never an apparently exact chart.
func complete(ctx context.Context, tx *sql.Tx, start, end time.Time, cutoff string) (bool, error) {
	if end.After(time.Now()) {
		return false, nil
	}
	var complete bool
	err := tx.QueryRowContext(ctx, `SELECT NOT EXISTS(SELECT 1 FROM writer_state WHERE incomplete<>0 OR lost_details<>0 OR snapshot_sequence>event_watermark) AND ?>=(SELECT value FROM storage_meta WHERE key=?)`, start.UnixMicro(), cutoff).Scan(&complete)
	if err != nil || !complete {
		return false, err
	}
	rows, err := tx.QueryContext(ctx, "SELECT coverage_start,coverage_end FROM writer_state WHERE coverage_end>? AND coverage_start<? ORDER BY coverage_start", start.UnixMicro(), end.UnixMicro())
	if err != nil {
		return false, err
	}
	defer rows.Close()
	covered := start.UnixMicro()
	for rows.Next() {
		var s, e int64
		if err = rows.Scan(&s, &e); err != nil {
			return false, err
		}
		if s > covered {
			return false, nil
		}
		if e > covered {
			covered = e
		}
		if covered >= end.UnixMicro() {
			return true, nil
		}
	}
	return false, rows.Err()
}

type Summary struct {
	Admitted, Blocked, FreshCache, StaleCache, Rejected, Duration uint64
	Complete                                                      bool
}

// Summary uses the longest retained resolution aligned with both boundaries,
// falling back to detail for sub-minute windows. Process lifetime snapshots are
// separately available through Counters and are never added to these totals.
func (d *DB) Summary(ctx context.Context, start, end time.Time) (Summary, error) {
	var s Summary
	if err := validateWindow(start, end); err != nil {
		return s, err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	source := "SELECT outcome,1 AS n,duration FROM query_events WHERE timestamp>=? AND timestamp<? AND timestamp >= (SELECT value FROM storage_meta WHERE key='detail_cutoff')"
	view, err := d.read.BeginTx(ctx, nil)
	if err != nil {
		return s, err
	}
	defer view.Rollback()
	cutoff := "detail_cutoff"
	args := []any{stats.AdmissionRejected, stats.PolicyBlock, stats.FreshCache, stats.StaleCache, stats.AdmissionRejected, stats.AdmissionRejected, start.UnixMicro(), end.UnixMicro()}
	for _, v := range []struct {
		width time.Duration
		key   string
	}{{24 * time.Hour, "day_cutoff"}, {time.Hour, "hour_cutoff"}, {time.Minute, "minute_cutoff"}} {
		if stats.UTCBucket(start.UnixMicro(), v.width) == start.UnixMicro() && stats.UTCBucket(end.UnixMicro(), v.width) == end.UnixMicro() {
			cutoff = v.key
			source = "SELECT outcome,count AS n,duration FROM rollups WHERE bucket>=? AND bucket<? AND resolution=? AND bucket+resolution*1000000>(SELECT value FROM storage_meta WHERE key=?)"
			args = append(args, int64(v.width/time.Second), cutoff)
			break
		}
	}
	err = view.QueryRowContext(ctx, `SELECT COALESCE(SUM(CASE WHEN outcome<>? THEN n ELSE 0 END),0),COALESCE(SUM(CASE WHEN outcome=? THEN n ELSE 0 END),0),COALESCE(SUM(CASE WHEN outcome=? THEN n ELSE 0 END),0),COALESCE(SUM(CASE WHEN outcome=? THEN n ELSE 0 END),0),COALESCE(SUM(CASE WHEN outcome=? THEN n ELSE 0 END),0),COALESCE(SUM(CASE WHEN outcome<>? THEN duration ELSE 0 END),0) FROM (`+source+`)`, args...).Scan(&s.Admitted, &s.Blocked, &s.FreshCache, &s.StaleCache, &s.Rejected, &s.Duration)
	if err != nil {
		return s, err
	}
	s.Complete, err = complete(ctx, view, start, end, cutoff)
	return s, err
}

func (d *DB) Counters(ctx context.Context, boot string) (stats.Snapshot, error) {
	var s stats.Snapshot
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var blob []byte
	err := d.read.QueryRowContext(ctx, "SELECT snapshot FROM writer_state WHERE boot_id=?", boot).Scan(&blob)
	if err != nil {
		return s, err
	}
	if len(blob) == 0 {
		return s, nil
	}
	err = json.Unmarshal(blob, &s)
	return s, err
}

// Rankings returns top ten exact clients and blocked domains, with stable byte
// ties. Hour-aligned windows use independently retained exact hourly dimensions;
// other windows use detail and its retention/completeness boundary.
func (d *DB) Rankings(ctx context.Context, start, end time.Time) (stats.Rankings, error) {
	r := stats.Rankings{Clients: []stats.Ranking{}, BlockedDomains: []stats.Ranking{}}
	if err := validateWindow(start, end); err != nil {
		return r, err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	s, e := start.UnixMicro(), end.UnixMicro()
	hourly := stats.UTCBucket(s, time.Hour) == s && stats.UTCBucket(e, time.Hour) == e
	view, err := d.read.BeginTx(ctx, nil)
	if err != nil {
		return r, err
	}
	defer view.Rollback()
	for kind := 0; kind < 2; kind++ {
		var q string
		var args []any
		if hourly {
			q = "SELECT key,SUM(count) n FROM rankings_hour WHERE bucket>=? AND bucket<? AND kind=? AND bucket+3600000000>(SELECT value FROM storage_meta WHERE key='hour_cutoff') GROUP BY key ORDER BY n DESC,key LIMIT 10"
			args = []any{s, e, kind}
		} else if kind == 0 {
			q = "SELECT c.address,COUNT(*) n FROM query_events e JOIN clients c ON c.id=e.client_id WHERE timestamp>=? AND timestamp<? AND outcome<>? AND timestamp >= (SELECT value FROM storage_meta WHERE key='detail_cutoff') GROUP BY c.address ORDER BY n DESC,c.address LIMIT 10"
			args = []any{s, e, stats.AdmissionRejected}
		} else {
			q = "SELECT d.name,COUNT(*) n FROM query_events e JOIN domains d ON d.id=e.domain_id WHERE timestamp>=? AND timestamp<? AND outcome=? AND timestamp >= (SELECT value FROM storage_meta WHERE key='detail_cutoff') GROUP BY d.name ORDER BY n DESC,d.name LIMIT 10"
			args = []any{s, e, stats.PolicyBlock}
		}
		rows, err := view.QueryContext(ctx, q, args...)
		if err != nil {
			return r, err
		}
		for rows.Next() {
			var key []byte
			var n uint64
			if err = rows.Scan(&key, &n); err != nil {
				rows.Close()
				return r, err
			}
			v := stats.Ranking{Key: string(key), Count: n}
			if kind == 0 {
				r.Clients = append(r.Clients, v)
			} else {
				r.BlockedDomains = append(r.BlockedDomains, v)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return r, err
		}
	}
	cutoff := "detail_cutoff"
	if hourly {
		cutoff = "hour_cutoff"
	}
	r.Complete, err = complete(ctx, view, start, end, cutoff)
	return r, err
}

type Point struct {
	Timestamp int64
	Outcomes  [stats.OutcomeCount]uint64
	Duration  uint64
	Histogram [8]uint64
}
type Series struct {
	Points   []Point
	Complete bool
}

// Timeseries uses aligned minute/hour/day boundaries and at most 1500 points.
// Histogram upper boundaries (exclusive), microseconds: 100,500,1000,5000,
// 10000,100000,1000000,+Inf. Histogram includes all terminal outcomes.
func (d *DB) Timeseries(ctx context.Context, start, end time.Time, width time.Duration) (Series, error) {
	r := Series{Points: []Point{}}
	cutoff := ""
	switch width {
	case time.Minute:
		cutoff = "minute_cutoff"
	case time.Hour:
		cutoff = "hour_cutoff"
	case 24 * time.Hour:
		cutoff = "day_cutoff"
	default:
		return r, errors.New("unsupported resolution")
	}
	if err := validateWindow(start, end); err != nil {
		return r, err
	}
	s, e := start.UnixMicro(), end.UnixMicro()
	if stats.UTCBucket(s, width) != s || stats.UTCBucket(e, width) != e || end.Sub(start)/width > 1500 {
		return r, errors.New("unaligned or oversized timeseries")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	for t := s; t < e; t += width.Microseconds() {
		r.Points = append(r.Points, Point{Timestamp: t})
	}
	view, err := d.read.BeginTx(ctx, nil)
	if err != nil {
		return r, err
	}
	defer view.Rollback()
	rows, err := view.QueryContext(ctx, "SELECT bucket,outcome,count,duration,h0,h1,h2,h3,h4,h5,h6,h7 FROM rollups WHERE resolution=? AND bucket>=? AND bucket<? AND bucket+resolution*1000000>(SELECT value FROM storage_meta WHERE key=?) ORDER BY bucket", int64(width/time.Second), s, e, cutoff)
	if err != nil {
		return r, err
	}
	for rows.Next() {
		var t int64
		var o stats.Outcome
		var n, duration uint64
		var h [8]uint64
		if err = rows.Scan(&t, &o, &n, &duration, &h[0], &h[1], &h[2], &h[3], &h[4], &h[5], &h[6], &h[7]); err != nil {
			rows.Close()
			return r, err
		}
		if o >= stats.OutcomeCount {
			rows.Close()
			return r, errors.New("corrupt rollup outcome")
		}
		p := &r.Points[(t-s)/width.Microseconds()]
		p.Outcomes[o] += n
		p.Duration += duration
		for i, v := range h {
			p.Histogram[i] += v
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return r, err
	}
	r.Complete, err = complete(ctx, view, start, end, cutoff)
	return r, err
}
