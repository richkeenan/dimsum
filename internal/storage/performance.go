package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/richkeenan/dimsum/internal/stats"
)

// Latency aggregates admitted terminal outcomes; rejected admissions do not
// represent resolution time. Fine.Count can be less than Count for legacy data.
type Latency struct {
	Count, Duration uint64
	Histogram       [8]uint64
	Fine            stats.LatencyHistogram
}

type PerformancePoint struct {
	Latency
	Timestamp int64
	Complete  bool
}

type Performance struct {
	Summary  Latency
	Outcomes [stats.AdmissionRejected]Latency
	Points   []PerformancePoint
	Complete bool
}

func (l *Latency) add(other *Latency) {
	l.Count += other.Count
	l.Duration += other.Duration
	for i, n := range other.Histogram {
		l.Histogram[i] += n
	}
	for i, n := range other.Fine {
		l.Fine[i] += n
	}
}

// Performance reads a single consistent snapshot. Full chart buckets use their
// independently retained rollups; partial edges use only exact retained detail.
// Fine histograms are merged before percentiles are calculated by the presenter.
func (d *DB) Performance(ctx context.Context, start, end time.Time, width time.Duration) (Performance, error) {
	r := Performance{Points: []PerformancePoint{}, Complete: true}
	cutoff := map[time.Duration]string{time.Minute: "minute_cutoff", time.Hour: "hour_cutoff", 24 * time.Hour: "day_cutoff"}[width]
	if cutoff == "" {
		return r, errors.New("unsupported resolution")
	}
	if err := validateWindow(start, end); err != nil {
		return r, err
	}
	first := stats.UTCBucket(start.UnixMicro(), width)
	last := stats.UTCBucket(end.Add(-time.Microsecond).UnixMicro(), width)
	if (last-first)/width.Microseconds()+1 > 1500 {
		return r, errors.New("performance allows at most 1500 buckets")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	view, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return r, err
	}
	defer view.Rollback()
	coverage, err := readPerformanceCoverage(ctx, view, start, end, cutoff)
	if err != nil {
		return r, err
	}
	for t := first; t <= last; t += width.Microseconds() {
		s, e := max(t, start.UnixMicro()), min(t+width.Microseconds(), end.UnixMicro())
		partial := s != t || e != t+width.Microseconds()
		complete := coverage.covers(s, e, partial)
		r.Points = append(r.Points, PerformancePoint{Timestamp: s, Complete: complete})
		r.Complete = r.Complete && complete
	}
	fullStart := first
	if fullStart < start.UnixMicro() {
		fullStart += width.Microseconds()
	}
	fullEnd := stats.UTCBucket(end.UnixMicro(), width)
	if fullStart < fullEnd {
		if err = r.readPerformanceRollups(ctx, view, fullStart, fullEnd, first, width, cutoff); err != nil {
			return r, err
		}
	}
	// There are at most two partial edges, including the single-bucket case.
	for i := range r.Points {
		t := first + int64(i)*width.Microseconds()
		s, e := max(t, start.UnixMicro()), min(t+width.Microseconds(), end.UnixMicro())
		if s != t || e != t+width.Microseconds() {
			if err = r.readPerformanceDetail(ctx, view, i, s, e); err != nil {
				return r, err
			}
		}
	}
	for i := range r.Points {
		r.Summary.add(&r.Points[i].Latency)
	}
	return r, nil
}

func (r *Performance) readPerformanceRollups(ctx context.Context, tx *sql.Tx, start, end, first int64, width time.Duration, cutoff string) error {
	rows, err := tx.QueryContext(ctx, `SELECT bucket,outcome,count,duration,h0,h1,h2,h3,h4,h5,h6,h7 FROM rollups
 WHERE resolution=? AND bucket>=? AND bucket<? AND outcome<?
 AND bucket+resolution*1000000>(SELECT value FROM storage_meta WHERE key=?)`, int64(width/time.Second), start, end, stats.AdmissionRejected, cutoff)
	if err != nil {
		return err
	}
	for rows.Next() {
		var timestamp int64
		var outcome stats.Outcome
		var l Latency
		if err = rows.Scan(&timestamp, &outcome, &l.Count, &l.Duration, &l.Histogram[0], &l.Histogram[1], &l.Histogram[2], &l.Histogram[3], &l.Histogram[4], &l.Histogram[5], &l.Histogram[6], &l.Histogram[7]); err != nil {
			rows.Close()
			return err
		}
		if outcome >= stats.AdmissionRejected {
			rows.Close()
			return errors.New("invalid latency outcome")
		}
		r.Points[(timestamp-first)/width.Microseconds()].Latency.add(&l)
		r.Outcomes[outcome].add(&l)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	rows, err = tx.QueryContext(ctx, `SELECT bucket,outcome,bin,count FROM latency_bins
 WHERE resolution=? AND bucket>=? AND bucket<? AND outcome<?
 AND bucket+resolution*1000000>(SELECT value FROM storage_meta WHERE key=?)`, int64(width/time.Second), start, end, stats.AdmissionRejected, cutoff)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var timestamp int64
		var outcome stats.Outcome
		var bin int
		var count uint64
		if err = rows.Scan(&timestamp, &outcome, &bin, &count); err != nil {
			return err
		}
		if outcome >= stats.AdmissionRejected || bin < 0 || bin >= len(stats.LatencyHistogram{}) {
			return errors.New("invalid latency bin")
		}
		r.Points[(timestamp-first)/width.Microseconds()].Fine[bin] += count
		r.Outcomes[outcome].Fine[bin] += count
	}
	return rows.Err()
}

func (r *Performance) readPerformanceDetail(ctx context.Context, tx *sql.Tx, index int, start, end int64) error {
	rows, err := tx.QueryContext(ctx, `SELECT outcome,duration,COUNT(*) FROM query_events
 WHERE timestamp>=? AND timestamp<? AND outcome<?
 AND timestamp>=(SELECT value FROM storage_meta WHERE key='detail_cutoff') GROUP BY outcome,duration`, start, end, stats.AdmissionRejected)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var outcome stats.Outcome
		var duration uint32
		var count uint64
		if err = rows.Scan(&outcome, &duration, &count); err != nil {
			return err
		}
		if outcome >= stats.AdmissionRejected {
			return errors.New("invalid latency outcome")
		}
		for _, l := range []*Latency{&r.Points[index].Latency, &r.Outcomes[outcome]} {
			l.Count += count
			l.Duration += uint64(duration) * count
			l.Histogram[stats.HistogramIndex(duration)] += count
			l.Fine[stats.LatencyIndex(duration)] += count
		}
	}
	return rows.Err()
}

// Fetch coverage once, rather than issuing queries per chart point. These are
// the same conservative loss/retention rules as complete(), scoped per point.
type performanceCoverage struct {
	valid                           bool
	rollupCutoff, detailCutoff, now int64
	spans                           [][2]int64
}

func readPerformanceCoverage(ctx context.Context, tx *sql.Tx, start, end time.Time, cutoff string) (performanceCoverage, error) {
	c := performanceCoverage{now: time.Now().UnixMicro()}
	err := tx.QueryRowContext(ctx, `SELECT
 NOT EXISTS(SELECT 1 FROM writer_state WHERE incomplete<>0 OR lost_details<>0 OR snapshot_sequence>event_watermark),
 (SELECT value FROM storage_meta WHERE key=?), (SELECT value FROM storage_meta WHERE key='detail_cutoff')`, cutoff).Scan(&c.valid, &c.rollupCutoff, &c.detailCutoff)
	if err != nil {
		return c, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT coverage_start,coverage_end FROM writer_state WHERE coverage_end>? AND coverage_start<? ORDER BY coverage_start`, start.UnixMicro(), end.UnixMicro())
	if err != nil {
		return c, err
	}
	defer rows.Close()
	for rows.Next() {
		var span [2]int64
		if err = rows.Scan(&span[0], &span[1]); err != nil {
			return c, err
		}
		c.spans = append(c.spans, span)
	}
	return c, rows.Err()
}

func (c performanceCoverage) covers(start, end int64, partial bool) bool {
	cutoff := c.rollupCutoff
	if partial {
		cutoff = c.detailCutoff
	}
	if !c.valid || start < cutoff || end > c.now {
		return false
	}
	for _, span := range c.spans {
		if span[0] > start {
			return false
		}
		start = max(start, span[1])
		if start >= end {
			return true
		}
	}
	return false
}
