package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/richkeenan/dimsum/internal/config"
	"github.com/richkeenan/dimsum/internal/stats"
	"github.com/richkeenan/dimsum/internal/storage"
	"github.com/richkeenan/dimsum/internal/transport"
	"github.com/richkeenan/dimsum/internal/upstream"
)

type ruleNote struct {
	generation, id uint32
	size           int
	text           [4096]byte
}
type aliasNote struct {
	sequence uint64
	size     uint8
	wire     [255]byte
}

// SQL and formatting run only in the consumer. The DNS-side auxiliary history
// cache is fixed at 256 descriptions (about 1 MiB); collisions mean unavailable
// explanation metadata, never another rule's explanation.
type observability struct {
	collector *stats.Collector
	db        *storage.DB
	boot      string
	openError error
	cancel    context.CancelFunc
	done      chan struct{}
	mu        sync.Mutex
	rules     [256]ruleNote
	aliases   [4096]aliasNote
}

func newObservability(store *config.Store) (*observability, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, err
	}
	o := &observability{collector: stats.New(4096), boot: hex.EncodeToString(id[:]), done: make(chan struct{})}
	dir := store.ResolvePath(store.Snapshot().Config().Paths.DataDir)
	if err := os.MkdirAll(dir, 0700); err != nil {
		o.openError = err
	} else {
		o.db, o.openError = storage.Open(filepath.Join(dir, "history.sqlite"))
	}
	return o, nil
}

func (o *observability) start() {
	ctx, cancel := context.WithCancel(context.Background())
	o.cancel = cancel
	if o.db == nil {
		close(o.done)
		return
	}
	go func() { defer close(o.done); _ = o.db.Run(ctx, o.collector, o.boot, o.enrich) }()
}
func (o *observability) close() {
	if o.cancel != nil {
		o.cancel()
		<-o.done
	}
	if o.db != nil {
		o.db.Close()
	}
}
func (o *observability) retention(c config.Statistics) {
	if o.db == nil {
		return
	}
	_ = o.db.SetRetention(storage.Retention{Detail: time.Duration(c.DetailDays) * 24 * time.Hour, Minute: time.Duration(c.MinuteDays) * 24 * time.Hour, Hour: time.Duration(c.HourDays) * 24 * time.Hour, Day: time.Duration(c.DayDays) * 24 * time.Hour})
}
func (o *observability) exchange(r upstream.ExchangeResult, _ error) {
	o.collector.RecordExchange(r.Attempts, r.Probes, r.Background)
}

func (o *observability) observe(r *transport.Request, result transport.Result) {
	if !result.Admitted && result.Outcome != transport.AdmissionRejected {
		o.collector.RecordMalformed()
		return
	}
	outcome := stats.ResolutionError
	switch result.Outcome {
	case transport.LocalAnswer:
		outcome = stats.LocalAnswer
	case transport.PolicyBlock:
		outcome = stats.PolicyBlock
	case transport.FreshAnswer:
		outcome = stats.FreshCache
	case transport.StaleAnswer:
		outcome = stats.StaleCache
	case transport.ForwardedAnswer:
		outcome = stats.ForwardedAnswer
	case transport.AdmissionRejected:
		outcome = stats.AdmissionRejected
	}
	arrival := result.Arrival
	if arrival.IsZero() {
		arrival = time.Now()
	}
	e := stats.QueryEvent{Timestamp: arrival.UnixMicro(), Duration: stats.SaturateMicros(uint64(max(0, result.Elapsed.Microseconds()))), Outcome: outcome, RCode: result.RCode, UpstreamID: result.UpstreamID}
	if result.Generation <= math.MaxUint32 {
		e.Generation = uint32(result.Generation)
		e.RuleID = result.RuleNumber
	}
	if r != nil {
		if r.Peer.Addr().IsValid() {
			e.Client = r.Peer.Addr().Unmap().As16()
		}
		q := &r.Message.Question
		e.QNameLength = uint8(min(int(q.Name.Length), len(e.QName)))
		copy(e.QName[:], q.Name.Canonical[:e.QNameLength])
		e.QType = q.Type
		e.QClass = q.Class
		if r.TCP {
			e.Flags |= stats.FlagTCP
		}
	}
	if result.Coalesced {
		e.Flags |= stats.FlagCoalesced
	}
	if result.Fallback {
		e.Flags |= stats.FlagFallbackUsed
	}
	if result.Truncated {
		e.Flags |= stats.FlagTruncated
	}
	if result.ResponsePolicy {
		e.Flags |= stats.FlagResponsePolicyBlocked
	}
	if e.RuleID != 0 {
		o.noteRule(e.Generation, e.RuleID, result)
	}
	if result.AliasLength > 0 {
		o.collector.RecordWith(e, func(sequence uint64) {
			if !o.mu.TryLock() {
				return
			}
			defer o.mu.Unlock()
			n := &o.aliases[sequence%uint64(len(o.aliases))]
			n.sequence = sequence
			n.size = result.AliasLength
			copy(n.wire[:], result.Alias[:result.AliasLength])
		})
	} else {
		o.collector.Record(e)
	}
}

func ruleSlot(generation, id uint32) uint32 { return (generation*16777619 + id) % 256 }
func (o *observability) noteRule(generation, id uint32, result transport.Result) {
	if !o.mu.TryLock() {
		return
	}
	defer o.mu.Unlock()
	n := &o.rules[ruleSlot(generation, id)]
	if n.generation == generation && n.id == id {
		return
	}
	n.generation = generation
	n.id = id
	n.size = 0
	write := func(key, value string) {
		for _, part := range []string{key, ": ", value, "\n"} {
			n.size += copy(n.text[n.size:], part)
		}
	}
	write("id", result.Rule.ID)
	write("kind", string(result.Rule.Kind))
	write("class", string(result.Rule.Class))
	write("pattern", result.Rule.Pattern)
	write("source", result.Rule.SourceID)
	if n.size == len(n.text) {
		copy(n.text[len(n.text)-len("\n[truncated]"):], "\n[truncated]")
	}
}

func (o *observability) enrich(events []stats.QueryEvent) (storage.BatchOptions, error) {
	var out storage.BatchOptions
	seen := make(map[[2]uint32]bool)
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, e := range events {
		a := &o.aliases[e.Sequence%uint64(len(o.aliases))]
		if a.sequence == e.Sequence && a.size > 0 {
			if out.Aliases == nil {
				out.Aliases = make(map[uint64][]byte)
			}
			out.Aliases[e.Sequence] = append([]byte(nil), a.wire[:a.size]...)
		}
		key := [2]uint32{e.Generation, e.RuleID}
		if e.RuleID == 0 || seen[key] {
			continue
		}
		seen[key] = true
		n := &o.rules[ruleSlot(e.Generation, e.RuleID)]
		if n.generation == e.Generation && n.id == e.RuleID {
			out.Rules = append(out.Rules, storage.RuleVersion{Generation: e.Generation, RuleID: e.RuleID, Description: string(n.text[:n.size])})
		}
	}
	return out, nil
}

func (o *observability) status() any {
	if o.openError != nil {
		return map[string]any{"available": false, "error": fmt.Sprint(o.openError)}
	}
	return map[string]any{"available": true, "writer": o.db.Status()}
}
