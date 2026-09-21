package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/richkeenan/dimsum/internal/stats"
)

// QueryByID returns one retained event, with explanations scoped to its boot.
// sql.ErrNoRows also covers events hidden by the logical retention cutoff.
func (d *DB) QueryByID(ctx context.Context, id int64) (Row, error) {
	var row Row
	if id <= 0 {
		return row, sql.ErrNoRows
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var client, name []byte
	e := &row.Event
	err := d.read.QueryRowContext(ctx, `SELECT e.id,e.boot_id,e.sequence,e.timestamp,e.duration,e.generation,c.address,n.name,e.qtype,e.qclass,e.outcome,e.rcode,e.upstream_id,e.rule_id,e.flags,e.alias,COALESCE(r.description,'')
 FROM query_events e JOIN domains n ON n.id=e.domain_id JOIN clients c ON c.id=e.client_id
 LEFT JOIN rule_versions r ON r.boot_id=e.boot_id AND r.generation=e.generation AND r.rule_id=e.rule_id
 WHERE e.id=? AND e.timestamp >= (SELECT value FROM storage_meta WHERE key='detail_cutoff')`, id).Scan(&row.ID, &row.Boot, &e.Sequence, &e.Timestamp, &e.Duration, &e.Generation, &client, &name, &e.QType, &e.QClass, &e.Outcome, &e.RCode, &e.UpstreamID, &e.RuleID, &e.Flags, &row.Alias, &row.RuleDescription)
	if err != nil {
		return row, err
	}
	if len(client) != 16 || len(name) > 255 || (len(name) == 0 && e.Outcome != stats.AdmissionRejected) {
		return Row{}, errors.New("invalid retained event identity")
	}
	copy(e.Client[:], client)
	copy(e.QName[:], name)
	e.QNameLength = uint8(len(name))
	return row, nil
}

type ObservedClient struct {
	Address        [16]byte
	Count, Blocked uint64
	LastSeen       time.Time
}
type ClientPage struct {
	Items     []ObservedClient
	Complete  bool
	Truncated bool
}

// GetClients aggregates retained detail, with a stable address tie-breaker and
// a finite page. Truncated is explicit; this endpoint is not a device inventory.
func (d *DB) GetClients(ctx context.Context, start, end time.Time, limit int) (ClientPage, error) {
	result := ClientPage{Items: []ObservedClient{}}
	if e := validateWindow(start, end); e != nil {
		return result, e
	}
	if limit < 1 || limit > 200 {
		return result, errors.New("client limit must be 1–200")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	rows, e := d.read.QueryContext(ctx, `SELECT c.address,COUNT(*),SUM(CASE WHEN e.outcome=? THEN 1 ELSE 0 END),MAX(e.timestamp)
 FROM query_events e JOIN clients c ON c.id=e.client_id
 WHERE e.timestamp>=? AND e.timestamp<? AND e.outcome<>? AND e.timestamp >= (SELECT value FROM storage_meta WHERE key='detail_cutoff')
 GROUP BY c.address ORDER BY COUNT(*) DESC,c.address LIMIT ?`, stats.PolicyBlock, start.UnixMicro(), end.UnixMicro(), stats.AdmissionRejected, limit+1)
	if e != nil {
		return result, e
	}
	for rows.Next() {
		var item ObservedClient
		var address []byte
		var timestamp int64
		if e = rows.Scan(&address, &item.Count, &item.Blocked, &timestamp); e != nil {
			rows.Close()
			return result, e
		}
		if len(address) != 16 {
			rows.Close()
			return result, errors.New("invalid retained client identity")
		}
		copy(item.Address[:], address)
		item.LastSeen = time.UnixMicro(timestamp).UTC()
		result.Items = append(result.Items, item)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return result, e
	}
	if len(result.Items) > limit {
		result.Items = result.Items[:limit]
		result.Truncated = true
	}
	// Use Query's public completeness contract so coverage and retention fixes
	// remain centralized in storage rather than being duplicated in this adapter.
	page, e := d.Query(ctx, QueryOptions{Start: start, End: end, Limit: 1})
	if e != nil {
		return result, e
	}
	result.Complete = page.Complete
	return result, nil
}

// PartialSeriesPoint aggregates the exact partial edge of a chart bucket from
// retained detail. It never includes events outside the requested half-open
// interval, and cannot claim complete coverage after detail retention.
func (d *DB) PartialSeriesPoint(ctx context.Context, start, end time.Time) (Point, bool, error) {
	point := Point{Timestamp: start.UnixMicro()}
	if e := validateWindow(start, end); e != nil {
		return point, false, e
	}
	if end.Sub(start) > 24*time.Hour {
		return point, false, errors.New("partial chart bucket exceeds one day")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	rows, e := d.read.QueryContext(ctx, `SELECT outcome,COUNT(*),SUM(duration),
 SUM(CASE WHEN duration<100 THEN 1 ELSE 0 END),
 SUM(CASE WHEN duration>=100 AND duration<500 THEN 1 ELSE 0 END),
 SUM(CASE WHEN duration>=500 AND duration<1000 THEN 1 ELSE 0 END),
 SUM(CASE WHEN duration>=1000 AND duration<5000 THEN 1 ELSE 0 END),
 SUM(CASE WHEN duration>=5000 AND duration<10000 THEN 1 ELSE 0 END),
 SUM(CASE WHEN duration>=10000 AND duration<100000 THEN 1 ELSE 0 END),
 SUM(CASE WHEN duration>=100000 AND duration<1000000 THEN 1 ELSE 0 END),
 SUM(CASE WHEN duration>=1000000 THEN 1 ELSE 0 END)
 FROM query_events WHERE timestamp>=? AND timestamp<? AND timestamp >= (SELECT value FROM storage_meta WHERE key='detail_cutoff') GROUP BY outcome`, start.UnixMicro(), end.UnixMicro())
	if e != nil {
		return point, false, e
	}
	for rows.Next() {
		var outcome stats.Outcome
		var count, duration uint64
		var histogram [8]uint64
		if e = rows.Scan(&outcome, &count, &duration, &histogram[0], &histogram[1], &histogram[2], &histogram[3], &histogram[4], &histogram[5], &histogram[6], &histogram[7]); e != nil {
			rows.Close()
			return point, false, e
		}
		if outcome >= stats.OutcomeCount {
			rows.Close()
			return point, false, errors.New("invalid retained outcome")
		}
		point.Outcomes[outcome] = count
		point.Duration += duration
		for i, count := range histogram {
			point.Histogram[i] += count
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return point, false, e
	}
	page, e := d.Query(ctx, QueryOptions{Start: start, End: end, Limit: 1})
	return point, page.Complete, e
}
