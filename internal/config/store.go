package config

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/richkeenan/dimsum/internal/localdns"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/richkeenan/dimsum/internal/lists"
	"github.com/richkeenan/dimsum/internal/policy"
)

const maxConfigBytes = 1 << 20
const maxRecoveryBytes = 256 << 20

type StoreOptions struct {
	Fetcher                       *lists.Fetcher
	PollInterval, RefreshInterval time.Duration
	Offline                       bool
}

// Store is the single coordinator for all control adapters. build admits one
// parser/compiler at a time, including Save and periodic refresh. DNS only loads
// active and never waits for these locks. Options are copied and then immutable.
type Store struct {
	path, state string
	options     StoreOptions
	build       sync.Mutex
	mu          sync.Mutex
	status      ActivationResult
	active      atomic.Pointer[Snapshot]
	starting    bool
}
type recovery struct {
	Generation uint64
	Revision   string
	Config     []byte
	Rules      []policy.Rule
	Sources    []SourceStatus
}

func OpenStore(ctx context.Context, path, state string, o StoreOptions) (*Store, error) {
	if o.PollInterval <= 0 {
		o.PollInterval = 100 * time.Millisecond
	}
	if o.RefreshInterval <= 0 {
		o.RefreshInterval = 24 * time.Hour
	}
	s := &Store{path: path, state: state, options: o, starting: true}
	defer func() { s.starting = false }()
	recoveryErr := s.recover()
	// Same saved bytes can immediately serve recovered rules without the Internet.
	if snap := s.Snapshot(); snap != nil {
		if b, e := readConfig(path); e == nil && revision(b) == snap.Revision() {
			s.mu.Lock()
			s.status.SavedRevision = snap.Revision()
			s.status.Recovered = false
			s.mu.Unlock()
			return s, nil
		}
	}
	if _, err := s.Reload(ctx); err != nil && s.Snapshot() == nil {
		return nil, fmt.Errorf("%w (recovery: %v)", err, recoveryErr)
	}
	return s, nil
}
func (s *Store) Snapshot() *Snapshot { return s.active.Load() }
func readConfig(path string) ([]byte, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return nil, e
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("configuration is not a regular file")
	}
	b, e := io.ReadAll(io.LimitReader(f, maxConfigBytes+1))
	if e != nil {
		return nil, e
	}
	if len(b) > maxConfigBytes {
		return nil, fmt.Errorf("configuration exceeds 1 MiB")
	}
	return b, nil
}
func (s *Store) Inspect() ActivationResult {
	s.mu.Lock()
	r := s.status
	r.Sources = append([]SourceStatus(nil), r.Sources...)
	s.mu.Unlock()
	if b, e := readConfig(s.path); e == nil {
		r.SavedRevision = revision(b)
	} else {
		r.SavedRevision = ""
		if r.Error == "" {
			r.Error = fmt.Sprintf("%s: %v", s.path, e)
		}
	}
	r.Pending = r.Pending || r.SavedRevision != r.ActiveRevision
	return r
}
func (s *Store) fail(err error) (ActivationResult, error) {
	err = fmt.Errorf("%s: %w", s.path, err)
	s.mu.Lock()
	s.status.Pending = false
	s.status.Error = err.Error()
	s.mu.Unlock()
	return s.Inspect(), err
}
func (s *Store) begin(rev string) {
	s.mu.Lock()
	s.status.SavedRevision = rev
	s.status.Pending = true
	s.status.Error = ""
	s.status.RestartRequired = false
	s.mu.Unlock()
}
func (s *Store) Reload(ctx context.Context) (ActivationResult, error) {
	s.build.Lock()
	defer s.build.Unlock()
	b, err := readConfig(s.path)
	if err != nil {
		return s.fail(err)
	}
	s.begin(revision(b))
	d, err := Parse(b)
	if err != nil {
		return s.fail(err)
	}
	return s.activate(ctx, d, revision(b), false)
}
func (s *Store) Save(ctx context.Context, expected string, d *Document) (ActivationResult, error) {
	s.build.Lock()
	defer s.build.Unlock()
	b, err := readConfig(s.path)
	if err != nil {
		return s.fail(err)
	}
	if revision(b) != expected {
		return s.fail(ErrConflict)
	}
	if d == nil {
		return s.fail(fmt.Errorf("nil candidate"))
	}
	s.begin(expected)
	return s.activate(ctx, d, expected, true)
}

func (s *Store) activate(ctx context.Context, d *Document, expected string, save bool) (ActivationResult, error) {
	if len(d.source) > maxConfigBytes {
		return s.fail(fmt.Errorf("configuration exceeds 1 MiB"))
	}
	c := d.Config()
	generation := uint64(1)
	if old := s.Snapshot(); old != nil {
		generation = old.generation + 1
		previous := old.Config()
		if !s.starting && (!reflect.DeepEqual(c.DNS, previous.DNS) || c.Admin != previous.Admin || c.Paths != previous.Paths) {
			s.mu.Lock()
			s.status.RestartRequired = true
			s.mu.Unlock()
			return s.fail(fmt.Errorf("dns/admin/paths change requires explicit service restart"))
		}
	}
	// Local-data behavior is a task-9 consumer: never report unsupported records active.
	local, err := localdns.Build(c.Zones, c.Records)
	if err != nil {
		return s.fail(err)
	}
	rules := c.PolicyRules()
	fetcher := s.options.Fetcher
	if fetcher == nil {
		var err error
		fetcher, err = lists.NewFetcherWithUpstreams(c.DNS.Upstreams)
		if err != nil {
			return s.fail(err)
		}
		defer fetcher.Client.CloseIdleConnections()
	}
	var statuses []SourceStatus
	var retained *recovery
	for _, sub := range c.Lists {
		if err := ctx.Err(); err != nil {
			return s.fail(err)
		}
		status := SourceStatus{ID: sub.ID, Enabled: sub.Enabled}
		if sub.Enabled {
			v, err := fetcher.Refresh(ctx, filepath.Join(s.state, "sources"), sub, s.options.Offline)
			prior, wasUsable := s.previousSource(sub)
			if err == nil && wasUsable && !sub.AllowLargeDeletion && len(v.Rules) <= prior.Rules/2 {
				err = fmt.Errorf("source deletion requires review: %d -> %d rules", prior.Rules, len(v.Rules))
				v = lists.Version{}
			}
			if err != nil && wasUsable {
				warning := err.Error()
				if retained == nil {
					previous, e := s.readRecovery()
					if e != nil {
						return s.fail(fmt.Errorf("source %s: cache and recovery unavailable: %w", sub.ID, e))
					}
					old := s.Snapshot()
					if previous.Generation != old.Generation() || previous.Revision != old.Revision() {
						return s.fail(fmt.Errorf("source %s: recovery generation mismatch", sub.ID))
					}
					retained = &previous
				}
				for _, rule := range retained.Rules {
					if rule.SourceID == sub.ID {
						v.Rules = append(v.Rules, rule)
					}
				}
				for _, status := range retained.Sources {
					if status.ID == sub.ID {
						v.SHA256 = status.SHA256
					}
				}
				if len(v.Rules) == 0 {
					return s.fail(fmt.Errorf("source %s: recovery membership missing", sub.ID))
				}
				v.Warning = "retained active source: " + warning
				err = nil
			}
			if err != nil {
				status.Error = err.Error()
			} else {
				if len(v.Rules) > policy.DefaultSnapshotOptions().MaxRules-len(rules) {
					return s.fail(fmt.Errorf("enabled source rule budget exceeded"))
				}
				status.Usable = true
				status.SHA256 = v.SHA256
				status.Rules = len(v.Rules)
				status.Error = v.Warning
				rules = append(rules, v.Rules...)
			}
		}
		statuses = append(statuses, status)
	}
	compiled, err := policy.CompileSnapshot(generation, rules, policy.DefaultLimits())
	if err != nil {
		return s.fail(err)
	}
	if err = ctx.Err(); err != nil {
		return s.fail(err)
	}
	artifact := recovery{Generation: generation, Revision: d.Revision(), Config: d.Bytes(), Rules: rules, Sources: statuses}
	payload, err := json.Marshal(artifact)
	if err != nil {
		return s.fail(err)
	}
	if len(payload) > maxRecoveryBytes {
		return s.fail(fmt.Errorf("recovery input artifact exceeds 256 MiB"))
	}
	// Stage the complete recovery unit before touching either published pointer.
	stage := filepath.Join(s.state, "candidate.artifact")
	if err = lists.WriteArtifact(stage, payload); err != nil {
		return s.fail(err)
	}
	defer os.Remove(stage)
	if save {
		if err = Publish(s.path, expected, d); err != nil {
			return s.fail(err)
		}
		expected = d.Revision()
	}
	current, err := readConfig(s.path)
	if err != nil {
		return s.fail(err)
	}
	if revision(current) != expected {
		return s.fail(ErrConflict)
	}
	if err = ctx.Err(); err != nil {
		return s.fail(err)
	}
	if err = os.Rename(stage, filepath.Join(s.state, "active.artifact")); err != nil {
		return s.fail(err)
	}
	dir, err := os.Open(s.state)
	if err != nil {
		return s.fail(err)
	}
	err = dir.Sync()
	_ = dir.Close()
	if err != nil {
		return s.fail(err)
	}
	s.publish(&Snapshot{document: d, policy: compiled, local: local, generation: generation}, statuses, false)
	return s.Inspect(), nil
}
func (s *Store) publish(snap *Snapshot, sources []SourceStatus, recovered bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active.Store(snap)
	s.status = ActivationResult{SavedRevision: snap.Revision(), ActiveRevision: snap.Revision(), ActiveGeneration: snap.generation, Recovered: recovered, Sources: sources}
	for _, source := range sources {
		if source.Enabled && source.Usable {
			s.status.SubscriptionAvailable = true
		}
	}
}
func (s *Store) recover() error {
	a, err := s.readRecovery()
	if err != nil {
		return err
	}
	d, err := Parse(a.Config)
	if err != nil {
		return err
	}
	compiled, err := policy.CompileSnapshot(a.Generation, a.Rules, policy.DefaultLimits())
	if err != nil {
		return err
	}
	local, err := localdns.Build(d.value.Zones, d.value.Records)
	if err != nil {
		return err
	}
	s.publish(&Snapshot{document: d, policy: compiled, local: local, generation: a.Generation}, a.Sources, true)
	return nil
}

func (s *Store) readRecovery() (recovery, error) {
	b, err := lists.ReadArtifact(filepath.Join(s.state, "active.artifact"), maxRecoveryBytes)
	if err != nil {
		return recovery{}, err
	}
	var a recovery
	if err = json.Unmarshal(b, &a); err != nil {
		return recovery{}, err
	}
	if a.Generation == 0 || a.Revision != revision(a.Config) || len(a.Config) > maxConfigBytes {
		return recovery{}, fmt.Errorf("recovery: invalid configuration identity")
	}
	return a, nil
}

func (s *Store) previousSource(sub lists.Subscription) (SourceStatus, bool) {
	old := s.Snapshot()
	if old == nil {
		return SourceStatus{}, false
	}
	same := false
	for _, prior := range old.document.value.Lists {
		if prior.ID == sub.ID && prior.URL == sub.URL && prior.Dialect == sub.Dialect && prior.DomainKind == sub.DomainKind && prior.Enabled {
			same = true
			break
		}
	}
	if !same {
		return SourceStatus{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, status := range s.status.Sources {
		if status.ID == sub.ID && status.Usable {
			return status, true
		}
	}
	return SourceStatus{}, false
}
