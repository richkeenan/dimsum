package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/richkeenan/dimsum/internal/queryresult"
	"github.com/richkeenan/dimsum/internal/stats"
)

const MaxBatch = 512

type RuleVersion struct {
	Generation, RuleID uint32
	Description        string
	SourceID           string // Exact archived identity; empty means unavailable.
}

// BatchOptions supplies an optional independent cumulative snapshot and immutable
// historical explanations. Aliases are canonical wire names indexed by sequence.
// Producers supplying matched aliases must arrange bounded consumer-side delivery.
type BatchOptions struct {
	Snapshot    *stats.Snapshot
	Rules       []RuleVersion
	Aliases     map[uint64][]byte
	Responses   map[uint64]*queryresult.Summary
	LostDetails uint64 // Cumulative consumer losses for this boot, replay-safe.
}

// WriteBatch atomically stores at most 512 events, rollups and watermarks. Events
// must have increasing nonzero sequences within a boot; replay at/below the last
// committed event watermark is ignored, including after retention. Snapshot.Version
// has its own watermark and is never added to event-derived totals.
func (d *DB) WriteBatch(ctx context.Context, boot string, events []stats.QueryEvent, options ...BatchOptions) error {
	if len(boot) == 0 || len(boot) > 128 || len(events) > MaxBatch || len(options) > 1 {
		return errors.New("invalid batch bounds")
	}
	var o BatchOptions
	if len(options) > 0 {
		o = options[0]
	}
	if len(o.Rules) > MaxBatch || len(o.Aliases) > MaxBatch || len(o.Responses) > MaxBatch || o.LostDetails > math.MaxInt64 {
		return errors.New("invalid metadata bounds")
	}
	for i, e := range events {
		if e.Sequence == 0 || e.Sequence > math.MaxInt64 || e.Outcome >= stats.OutcomeCount || (i > 0 && e.Sequence <= events[i-1].Sequence) {
			return errors.New("invalid event sequence or outcome")
		}
		if len(o.Aliases[e.Sequence]) > 255 {
			return errors.New("alias exceeds wire name bound")
		}
	}
	for _, r := range o.Rules {
		if len(r.Description) > 4096 || len(r.SourceID) > MaxSourceIDBytes {
			return errors.New("rule description too large")
		}
	}
	if o.Snapshot != nil && (o.Snapshot.Version > math.MaxInt64 || o.Snapshot.Sequence > math.MaxInt64 || o.Snapshot.ObservedEnd < o.Snapshot.ObservedStart) {
		return errors.New("snapshot watermark overflow")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	tx, err := d.write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "INSERT INTO writer_state(boot_id) VALUES(?) ON CONFLICT DO NOTHING", boot); err != nil {
		return err
	}
	var watermark uint64
	if err = tx.QueryRowContext(ctx, "SELECT event_watermark FROM writer_state WHERE boot_id=?", boot).Scan(&watermark); err != nil {
		return err
	}
	for _, r := range o.Rules {
		if _, err = tx.ExecContext(ctx, "INSERT INTO rule_versions(boot_id,generation,rule_id,description,source_id) VALUES(?,?,?,?,?) ON CONFLICT DO NOTHING", boot, r.Generation, r.RuleID, r.Description, r.SourceID); err != nil {
			return err
		}
		var text, source string
		if err = tx.QueryRowContext(ctx, "SELECT description,source_id FROM rule_versions WHERE boot_id=? AND generation=? AND rule_id=?", boot, r.Generation, r.RuleID).Scan(&text, &source); err != nil {
			return err
		}
		if text != r.Description || source != r.SourceID {
			return errors.New("immutable rule version conflict")
		}
	}
	insert, err := tx.PrepareContext(ctx, `INSERT INTO query_events(boot_id,sequence,timestamp,duration,domain_id,client_id,qtype,qclass,outcome,rcode,upstream_id,generation,rule_id,flags,alias,response) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer insert.Close()
	roll, err := tx.PrepareContext(ctx, `INSERT INTO rollups VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(resolution,bucket,outcome) DO UPDATE SET count=count+excluded.count,duration=duration+excluded.duration,h0=h0+excluded.h0,h1=h1+excluded.h1,h2=h2+excluded.h2,h3=h3+excluded.h3,h4=h4+excluded.h4,h5=h5+excluded.h5,h6=h6+excluded.h6,h7=h7+excluded.h7`)
	if err != nil {
		return err
	}
	defer roll.Close()
	rank, err := tx.PrepareContext(ctx, `INSERT INTO rankings_hour VALUES(?,?,?,?) ON CONFLICT(bucket,kind,key) DO UPDATE SET count=count+excluded.count`)
	if err != nil {
		return err
	}
	defer rank.Close()
	var cutoffs [4]int64
	for i, key := range []string{"detail_cutoff", "minute_cutoff", "hour_cutoff", "day_cutoff"} {
		if err = tx.QueryRowContext(ctx, "SELECT value FROM storage_meta WHERE key=?", key).Scan(&cutoffs[i]); err != nil {
			return err
		}
	}
	// At most MaxBatch*3 sparse entries; aggregate off the DNS request path.
	type latencyKey struct {
		resolution, bucket int64
		outcome            stats.Outcome
		bin                int
	}
	latencies := make(map[latencyKey]uint64)
	type rollupKey struct {
		resolution, bucket int64
		outcome            stats.Outcome
	}
	type rollupValue struct {
		count, duration uint64
		histogram       [8]uint64
	}
	type rankingKey struct {
		bucket int64
		kind   int
		key    string
	}
	// Fold repeated aggregate rows in memory, bounded by MaxBatch, just as for
	// latency bins below. Detail rows and sequence/watermark handling stay per-event.
	rollups := make(map[rollupKey]rollupValue)
	rankings := make(map[rankingKey]uint64)
	domains, err := prepareDimension(ctx, tx, "domains", "name")
	if err != nil {
		return err
	}
	defer domains.close()
	clients, err := prepareDimension(ctx, tx, "clients", "address")
	if err != nil {
		return err
	}
	defer clients.close()
	for _, e := range events {
		if e.Sequence <= watermark {
			continue
		}
		if e.Sequence != watermark+1 {
			if _, err = tx.ExecContext(ctx, "UPDATE writer_state SET incomplete=1 WHERE boot_id=?", boot); err != nil {
				return err
			}
		}
		watermark = e.Sequence
		if e.Timestamp == math.MaxInt64 {
			return errors.New("event timestamp overflow")
		}
		// Event timestamps alone do not certify continuous observation. In
		// particular a delayed event must not bridge the gap to this boot's start.
		// Each tier has its own retention boundary; expired detail must not
		// discard a still-retained hourly or daily observation.
		if e.Timestamp >= cutoffs[0] {
			domain, er := domains.resolve(ctx, e.QName[:e.QNameLength])
			if er != nil {
				return er
			}
			client, er := clients.resolve(ctx, e.Client[:])
			if er != nil {
				return er
			}
			var response []byte
			if summary := o.Responses[e.Sequence]; summary != nil {
				if len(summary.Records) > queryresult.MaxRecords {
					return errors.New("response record bound exceeded")
				}
				response, err = json.Marshal(summary)
				if err != nil {
					return err
				}
				if len(response) > queryresult.MaxJSONBytes {
					return errors.New("response size bound exceeded")
				}
			}
			if _, err = insert.ExecContext(ctx, boot, e.Sequence, e.Timestamp, e.Duration, domain, client, e.QType, e.QClass, e.Outcome, e.RCode, e.UpstreamID, e.Generation, e.RuleID, e.Flags, o.Aliases[e.Sequence], response); err != nil {
				return err
			}
		}
		for i, width := range []time.Duration{time.Minute, time.Hour, 24 * time.Hour} {
			if stats.UTCBucket(e.Timestamp, width)+width.Microseconds() <= cutoffs[i+1] {
				continue
			}
			key := rollupKey{int64(width / time.Second), stats.UTCBucket(e.Timestamp, width), e.Outcome}
			value := rollups[key]
			value.count++
			value.duration += uint64(e.Duration)
			value.histogram[stats.HistogramIndex(e.Duration)]++
			rollups[key] = value
			if e.Outcome != stats.AdmissionRejected {
				latencies[latencyKey{int64(width / time.Second), stats.UTCBucket(e.Timestamp, width), e.Outcome, stats.LatencyIndex(e.Duration)}]++
			}
		}
		if e.Outcome != stats.AdmissionRejected && stats.UTCBucket(e.Timestamp, time.Hour)+time.Hour.Microseconds() > cutoffs[2] {
			rankings[rankingKey{stats.UTCBucket(e.Timestamp, time.Hour), 0, string(e.Client[:])}]++
		}
		if e.Outcome == stats.PolicyBlock && stats.UTCBucket(e.Timestamp, time.Hour)+time.Hour.Microseconds() > cutoffs[2] {
			rankings[rankingKey{stats.UTCBucket(e.Timestamp, time.Hour), 1, string(e.QName[:e.QNameLength])}]++
		}
	}
	for key, value := range rollups {
		h := value.histogram
		if _, err = roll.ExecContext(ctx, key.resolution, key.bucket, key.outcome, value.count, value.duration, h[0], h[1], h[2], h[3], h[4], h[5], h[6], h[7]); err != nil {
			return err
		}
	}
	for key, count := range rankings {
		if _, err = rank.ExecContext(ctx, key.bucket, key.kind, []byte(key.key), count); err != nil {
			return err
		}
	}
	latency, err := tx.PrepareContext(ctx, `INSERT INTO latency_bins VALUES(?,?,?,?,?) ON CONFLICT(resolution,bucket,outcome,bin) DO UPDATE SET count=count+excluded.count`)
	if err != nil {
		return err
	}
	defer latency.Close()
	for key, count := range latencies {
		if _, err = latency.ExecContext(ctx, key.resolution, key.bucket, key.outcome, key.bin, count); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, "UPDATE writer_state SET event_watermark=? WHERE boot_id=?", watermark, boot); err != nil {
		return err
	}
	if o.Snapshot != nil {
		blob, er := json.Marshal(o.Snapshot)
		if er != nil {
			return er
		}
		if _, err = tx.ExecContext(ctx, "UPDATE writer_state SET snapshot_watermark=?,snapshot_sequence=?,snapshot=?,incomplete=MAX(incomplete,?) WHERE boot_id=? AND snapshot_watermark<=?", o.Snapshot.Version, o.Snapshot.Sequence, blob, o.Snapshot.Dropped > 0, boot, o.Snapshot.Version); err != nil {
			return err
		}
		if o.Snapshot.ObservedEnd > o.Snapshot.ObservedStart {
			if _, err = tx.ExecContext(ctx, "UPDATE writer_state SET coverage_start=MIN(COALESCE(coverage_start,?),?),coverage_end=MAX(COALESCE(coverage_end,?),?) WHERE boot_id=? AND snapshot_watermark=?", o.Snapshot.ObservedStart, o.Snapshot.ObservedStart, o.Snapshot.ObservedEnd, o.Snapshot.ObservedEnd, boot, o.Snapshot.Version); err != nil {
				return err
			}
		}
	}
	if _, err = tx.ExecContext(ctx, "UPDATE writer_state SET lost_details=MAX(lost_details,?) WHERE boot_id=?", o.LostDetails, boot); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE storage_meta SET value=? WHERE key='last_write'", time.Now().UnixMicro()); err != nil {
		return err
	}
	return tx.Commit()
}

// Dictionary IDs are memoized only within this transaction: rollback and
// retention may invalidate them between batches. Memory is bounded by MaxBatch.
type batchDimension struct {
	insert, selectID *sql.Stmt
	ids              map[string]int64
}

func prepareDimension(ctx context.Context, tx *sql.Tx, table, column string) (*batchDimension, error) {
	insert, err := tx.PrepareContext(ctx, "INSERT INTO "+table+"("+column+") VALUES(?) ON CONFLICT DO NOTHING")
	if err != nil {
		return nil, err
	}
	selectID, err := tx.PrepareContext(ctx, "SELECT id FROM "+table+" WHERE "+column+"=?")
	if err != nil {
		_ = insert.Close()
		return nil, err
	}
	return &batchDimension{insert: insert, selectID: selectID, ids: make(map[string]int64)}, nil
}

func (d *batchDimension) close() {
	_ = d.insert.Close()
	_ = d.selectID.Close()
}

func (d *batchDimension) resolve(ctx context.Context, value []byte) (int64, error) {
	if id, ok := d.ids[string(value)]; ok {
		return id, nil
	}
	if _, err := d.insert.ExecContext(ctx, value); err != nil {
		return 0, err
	}
	var id int64
	err := d.selectID.QueryRowContext(ctx, value).Scan(&id)
	if err == nil {
		d.ids[string(value)] = id
	}
	return id, err
}

// Run is the single consumer. One bounded failed batch is discarded, reported,
// and counted on the next successful transaction; DNS producers never retry it.
// Cancellation flushes with an independent bounded context. Stop producers first
// when an exact final snapshot is required. The collector channel is never closed.
func (d *DB) Run(ctx context.Context, c *stats.Collector, boot string, enrich ...func([]stats.QueryEvent) (BatchOptions, error)) error {
	if c == nil || boot == "" || len(boot) > 128 || len(enrich) > 1 {
		return errors.New("invalid consumer arguments")
	}
	d.statusMu.Lock()
	if d.running {
		d.statusMu.Unlock()
		return errors.New("storage consumer already running")
	}
	d.running = true
	d.statusMu.Unlock()
	defer func() { d.statusMu.Lock(); d.running = false; d.statusMu.Unlock() }()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	maintenance := time.NewTicker(time.Minute)
	defer maintenance.Stop()
	catchup := time.NewTicker(20 * time.Millisecond)
	defer catchup.Stop()
	maintenanceDue := true
	batch := make([]stats.QueryEvent, 0, MaxBatch)
	var lost uint64
	flush := func(flushCtx context.Context) error {
		s := c.Snapshot()
		var options BatchOptions
		var err error
		if len(enrich) > 0 && len(batch) > 0 {
			options, err = enrich[0](batch)
		}
		options.Snapshot = &s
		options.LostDetails = lost
		if err == nil {
			err = d.WriteBatch(flushCtx, boot, batch, options)
		}
		d.statusMu.Lock()
		defer d.statusMu.Unlock()
		if err != nil {
			lost += uint64(len(batch))
			d.status.LostDetails += uint64(len(batch))
			d.status.LastError = err.Error()
		} else {
			d.status.LastError = ""
			d.status.LastSuccess = time.Now()
		}
		batch = batch[:0]
		return err
	}
	for {
		select {
		case e := <-c.Events():
			batch = append(batch, e)
			if len(batch) == MaxBatch {
				_ = flush(ctx)
			}
		case <-ticker.C:
			_ = flush(ctx)
		case <-maintenance.C:
			maintenanceDue = true
		case <-catchup.C:
			if !maintenanceDue {
				continue
			}
			// Explicitly yield to queued ingestion between maintenance batches.
			for n := len(c.Events()); n > 0 && len(batch) < MaxBatch; n-- {
				select {
				case e := <-c.Events():
					batch = append(batch, e)
				default:
				}
			}
			if len(batch) > 0 {
				_ = flush(ctx)
			}
			budget, cancelBudget := context.WithTimeout(ctx, 100*time.Millisecond)
			err := d.Retain(budget, time.Now(), d.Retention())
			cancelBudget()
			if err != nil {
				d.statusMu.Lock()
				d.status.LastError = fmt.Sprintf("retention: %v", err)
				d.statusMu.Unlock()
			}
			maintenanceDue = err != nil || d.Status().Backlogged
			if !maintenanceDue {
				if _, err := d.Checkpoint(ctx); err != nil {
					d.statusMu.Lock()
					d.status.LastError = fmt.Sprintf("checkpoint: %v", err)
					d.statusMu.Unlock()
				}
			}
		case <-ctx.Done():
			final, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			// Drain only the queue capacity observed at shutdown, not continuing producers.
			n := len(c.Events())
			for range n {
				select {
				case e := <-c.Events():
					batch = append(batch, e)
					if len(batch) == MaxBatch {
						if err := flush(final); err != nil {
							return err
						}
					}
				default:
				}
			}
			return flush(final)
		}
	}
}
