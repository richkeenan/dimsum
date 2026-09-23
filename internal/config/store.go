package config

import (
	"context"
	"fmt"
	"github.com/richkeenan/dimsum/internal/clients"
	"github.com/richkeenan/dimsum/internal/localdns"
	"io"
	"os"
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
	listeners   []string // actual bound addresses, guarded by build
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
	return s.reload(ctx, false)
}

// The changed-file path rechecks after acquiring build: a queued watcher may
// have observed Save's text publication before its memory publication.
func (s *Store) reload(ctx context.Context, changedOnly bool) (ActivationResult, error) {
	s.build.Lock()
	defer s.build.Unlock()
	b, err := readConfig(s.path)
	if err != nil {
		return s.fail(err)
	}
	if changedOnly {
		if active := s.Snapshot(); active != nil && active.Revision() == revision(b) {
			return s.Inspect(), nil
		}
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
	if len(s.listeners) > 0 {
		bound := c
		bound.DNS.Listen = s.listeners
		if err := Validate(bound); err != nil {
			return s.fail(err)
		}
	}
	generation := uint64(1)
	if old := s.Snapshot(); old != nil {
		generation = old.generation + 1
		previous := old.Config()
		if !s.starting && (!reflect.DeepEqual(c.DNS.Listen, previous.DNS.Listen) || c.Admin.Listen != previous.Admin.Listen || c.Admin.ControlSocket != previous.Admin.ControlSocket || c.Admin.SecureCookies != previous.Admin.SecureCookies || c.Paths != previous.Paths) {
			s.mu.Lock()
			s.status.RestartRequired = true
			s.mu.Unlock()
			return s.fail(fmt.Errorf("dns/admin/paths change requires explicit service restart"))
		}
		if !s.starting && (c.Cache.Bytes != previous.Cache.Bytes || c.Cache.Shards != previous.Cache.Shards || c.Cache.MaxNegativeTTLSeconds != previous.Cache.MaxNegativeTTLSeconds) {
			s.mu.Lock()
			s.status.RestartRequired = true
			s.mu.Unlock()
			return s.fail(fmt.Errorf("cache.bytes/cache.shards/cache.max_negative_ttl_seconds change requires explicit service restart"))
		}
	}
	// Compile every request-visible producer before staging the recovery unit.
	if err := s.validateDocumentSecrets(d); err != nil {
		return s.fail(err)
	}
	local, err := localdns.Build(c.Zones, c.Records)
	if err != nil {
		return s.fail(err)
	}
	names, err := clients.NewView(c.Naming, c.NamingOverrides(), local.Names())
	if err != nil {
		return s.fail(err)
	}
	subs, err := s.prepareSubscriptions(ctx, c, !save)
	if err != nil {
		return s.fail(err)
	}
	clientPolicies, err := c.compileClientPolicies(generation, subs.policy)
	if err != nil {
		return s.fail(err)
	}
	if err = ctx.Err(); err != nil {
		return s.fail(err)
	}
	stage, err := s.stageManifest(d, generation, subs)
	if err != nil {
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
	if err = s.commitManifest(stage); err != nil {
		return s.fail(err)
	}
	s.publish(&Snapshot{document: d, policy: clientPolicies.Network().Policy(), clientPolicies: clientPolicies, local: local, names: names, generation: generation, subscriptions: subs}, subs.sources, false)
	if !save {
		s.cleanupSubscriptions(subs.artifact.Name)
	}
	return s.Inspect(), nil
}

// Ordinary saves reuse the exact active membership of unchanged subscriptions.
// Reload still refreshes feeds. Read provenance from the immutable snapshot so
// edits need neither network access nor another retained copy of every rule.
func (s *Store) reusableSources(subs []lists.Subscription) map[string]lists.Version {
	old := s.Snapshot()
	if old == nil {
		return nil
	}
	reusable := make(map[string]lists.Version)
	for _, sub := range subs {
		if !sub.Enabled {
			continue
		}
		for _, prior := range old.document.value.Lists {
			prior.DefaultApply = nil
			sub.DefaultApply = nil
			if !reflect.DeepEqual(sub, prior) {
				continue
			}
			if status, usable := s.previousSource(sub); usable {
				reusable[sub.ID] = lists.Version{
					Rules:  make([]policy.Rule, 0, status.Rules),
					SHA256: status.SHA256, Warning: status.Error,
				}
			}
			break
		}
	}
	if len(reusable) == 0 {
		return reusable
	}
	for number := uint32(1); ; number++ {
		rule, ok := old.subscriptions.policy.RuleAt(number)
		if !ok {
			break
		}
		if rule.Class != policy.SubscriptionAllow && rule.Class != policy.SubscriptionDeny {
			continue
		}
		if version, ok := reusable[rule.SourceID]; ok {
			version.Rules = append(version.Rules, rule)
			reusable[rule.SourceID] = version
		}
	}
	return reusable
}
func (s *Store) publish(snap *Snapshot, sources []SourceStatus, recovered bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.active.Load()
	snap.routeLifetimes = make(map[string]routeLifetime)
	for _, route := range snap.clientPolicies.routes {
		var lifetime routeLifetime
		if old != nil {
			lifetime = old.routeLifetimes[route.id]
		}
		if lifetime.ctx == nil {
			lifetime.ctx, lifetime.cancel = context.WithCancel(context.Background())
		}
		snap.routeLifetimes[route.id] = lifetime
	}
	if old != nil && old.upstreamContext != nil && reflect.DeepEqual(old.UpstreamOptions(), snap.UpstreamOptions()) {
		snap.upstreamContext, snap.upstreamCancel = old.upstreamContext, old.upstreamCancel
	} else {
		snap.upstreamContext, snap.upstreamCancel = context.WithCancel(context.Background())
	}
	s.active.Store(snap)
	if old != nil {
		for id, lifetime := range old.routeLifetimes {
			if _, retained := snap.routeLifetimes[id]; !retained {
				lifetime.cancel()
			}
		}
	}
	if old != nil && old.upstreamContext != snap.upstreamContext && old.upstreamCancel != nil {
		old.upstreamCancel()
	}
	s.status = ActivationResult{SavedRevision: snap.Revision(), ActiveRevision: snap.Revision(), ActiveGeneration: snap.generation, Recovered: recovered, Sources: sources}
	for _, source := range sources {
		if source.Enabled && source.Usable {
			s.status.SubscriptionAvailable = true
		}
	}
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
