package storage

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/richkeenan/dimsum/internal/stats"
)

type aggregateSpan struct {
	start, end time.Time
	width      time.Duration // Zero means exact retained detail.
	cutoff     string
}

func aggregateCutoffs(ctx context.Context, tx *sql.Tx) (map[time.Duration]int64, error) {
	var minute, hour, day int64
	err := tx.QueryRowContext(ctx, `SELECT
 (SELECT value FROM storage_meta WHERE key='minute_cutoff'),
 (SELECT value FROM storage_meta WHERE key='hour_cutoff'),
 (SELECT value FROM storage_meta WHERE key='day_cutoff')`).Scan(&minute, &hour, &day)
	return map[time.Duration]int64{time.Minute: minute, time.Hour: hour, 24 * time.Hour: day}, err
}

// Partition without expanding the requested window. Each tier handles only
// full buckets; its two edges descend to a finer tier, ending in raw detail.
// This is bounded by the number of tiers, not the number of chart buckets.
func aggregateWindow(start, end time.Time, cutoffs map[time.Duration]int64, widths ...time.Duration) []aggregateSpan {
	if !start.Before(end) {
		return nil
	}
	if len(widths) == 0 {
		return []aggregateSpan{{start, end, 0, "detail_cutoff"}}
	}
	width := widths[0]
	first := start.Truncate(width)
	if first.Before(start) {
		first = first.Add(width)
	}
	// Retention tiers are independently configurable. An expired aggregate
	// must not hide still-available finer history. Keep a cutoff-overlapping
	// bucket (as retention does); its coverage remains conservatively partial.
	retained := time.UnixMicro(stats.UTCBucket(cutoffs[width], width))
	if retained.After(first) {
		first = retained
	}
	last := end.Truncate(width)
	if !first.Before(last) {
		return aggregateWindow(start, end, cutoffs, widths[1:]...)
	}
	cutoff := map[time.Duration]string{time.Minute: "minute_cutoff", time.Hour: "hour_cutoff", 24 * time.Hour: "day_cutoff"}[width]
	spans := aggregateWindow(start, first, cutoffs, widths[1:]...)
	spans = append(spans, aggregateSpan{first, last, width, cutoff})
	return append(spans, aggregateWindow(last, end, cutoffs, widths[1:]...)...)
}

func aggregateComplete(ctx context.Context, tx *sql.Tx, spans []aggregateSpan) (bool, error) {
	for _, span := range spans {
		ok, err := complete(ctx, tx, span.start, span.end, span.cutoff)
		if err != nil || !ok {
			return false, err
		}
	}
	return true, nil
}

func summarySource(spans []aggregateSpan) (string, []any) {
	var queries []string
	var args []any
	for _, span := range spans {
		if span.width == 0 {
			queries = append(queries, "SELECT outcome,1 AS n,duration FROM query_events WHERE timestamp>=? AND timestamp<? AND timestamp >= (SELECT value FROM storage_meta WHERE key='detail_cutoff')")
			args = append(args, span.start.UnixMicro(), span.end.UnixMicro())
		} else {
			queries = append(queries, "SELECT outcome,count AS n,duration FROM rollups WHERE bucket>=? AND bucket<? AND resolution=? AND bucket+resolution*1000000>(SELECT value FROM storage_meta WHERE key=?)")
			args = append(args, span.start.UnixMicro(), span.end.UnixMicro(), int64(span.width/time.Second), span.cutoff)
		}
	}
	return strings.Join(queries, " UNION ALL "), args
}

// All keys survive until the final grouping, so an identity below the top ten
// in each individual span can still win the combined ranking.
func rankingSource(spans []aggregateSpan, kind int) (string, []any) {
	var queries []string
	var args []any
	for _, span := range spans {
		args = append(args, span.start.UnixMicro(), span.end.UnixMicro())
		if span.width != 0 {
			queries = append(queries, "SELECT key,count AS n FROM rankings_hour WHERE bucket>=? AND bucket<? AND kind=? AND bucket+3600000000>(SELECT value FROM storage_meta WHERE key='hour_cutoff')")
			args = append(args, kind)
		} else if kind == 0 {
			queries = append(queries, "SELECT c.address AS key,COUNT(*) AS n FROM query_events e JOIN clients c ON c.id=e.client_id WHERE timestamp>=? AND timestamp<? AND outcome<>? AND timestamp >= (SELECT value FROM storage_meta WHERE key='detail_cutoff') GROUP BY c.address")
			args = append(args, stats.AdmissionRejected)
		} else {
			queries = append(queries, "SELECT d.name AS key,COUNT(*) AS n FROM query_events e JOIN domains d ON d.id=e.domain_id WHERE timestamp>=? AND timestamp<? AND outcome=? AND timestamp >= (SELECT value FROM storage_meta WHERE key='detail_cutoff') GROUP BY d.name")
			args = append(args, stats.PolicyBlock)
		}
	}
	return strings.Join(queries, " UNION ALL "), args
}
