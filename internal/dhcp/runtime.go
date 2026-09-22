package dhcp

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/insomniacslk/dhcp/dhcpv4"
)

// Link owns all packet resources; Close must unblock Receive and Send. Send is
// deadline bounded. Probe must honor cancellation (including during shutdown).
type Link interface {
	Receiver
	Send(context.Context, WireReply) error
	MTU() int
}

// LeaseWriter is the bounded durable-completion boundary. Submit is nonblocking;
// Results reports only known transaction outcomes. An uncertain write is latched
// through Err without a rollback completion. Close retains executing ownership.
type LeaseWriter interface {
	Submit(context.Context, Mutation) error
	Results() <-chan CommitResult
	Err() error
	Close(context.Context) error
}

// Projection is a detached immutable-by-convention copy of committed ownership.
// Consumers must check Enabled/Generation and filter Bound plus Expiry > now.
type Projection struct {
	Generation uint64
	Enabled    bool
	Settings   Settings
	Leases     []Lease
}

// LeaseView has no exported mutable storage. View identity changes only at a
// committed publication/configuration boundary. DNS can capture it without
// copying all leases; callers must not retain retired views indefinitely.
type LeaseView struct{ projection Projection }

func (v *LeaseView) Generation() uint64 { return v.projection.Generation }
func (v *LeaseView) Settings() Settings { return v.projection.Settings.Clone() }
func (v *LeaseView) Len() int           { return len(v.projection.Leases) }
func (v *LeaseView) Lease(i int) Lease  { return v.projection.Leases[i] }

type RuntimeStatus struct {
	Generation     uint64   `json:"generation"`
	Error          string   `json:"error,omitempty"`
	Storage        string   `json:"storage"`
	Capacity       int      `json:"capacity"`
	Held           int      `json:"held"`
	Pending        int      `json:"pending"`
	ClockSuspended bool     `json:"clock_suspended"`
	Transport      Counters `json:"transport"`
}

// LeaseSnapshot is a bounded inspection copy, not a live database transaction.
// Pagination cursors should include process boot ID, Generation and Revision.
type LeaseSnapshot struct {
	Generation uint64
	Revision   uint64
	Leases     []Lease
}
type applyRequest struct {
	ctx        context.Context
	settings   Settings
	generation uint64
	result     chan error
}
type Runtime struct {
	cancel        context.CancelFunc
	stopping      <-chan struct{}
	done          chan struct{}
	apply         chan applyRequest
	mu            sync.RWMutex
	status        RuntimeStatus
	projection    Projection
	leases        []Lease
	leaseRevision uint64
	transport     *Transport
	view          atomic.Pointer[LeaseView]
}

// StartRuntime takes ownership of link/store only on success. Recovery and
// topology validation finish before any packet or probe worker starts.
func StartRuntime(parent context.Context, s Settings, generation uint64, link Link, probe ProbeFunc, store LeaseWriter, recovery LeaseRecovery) (*Runtime, error) {
	if !s.Enabled || link == nil || probe == nil || store == nil {
		return nil, errors.New("dhcp: enabled settings, link, probe and store required")
	}
	e, err := NewEngine(s, generation, nil)
	if err != nil {
		return nil, err
	}
	if err = e.Restore(recovery.Leases, recovery.LastKnown); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	packets := make(chan Request) // transport's bounded queue is the only packet backlog
	t, err := NewTransport(link, func(ctx context.Context, p *dhcpv4.DHCPv4, peer netip.AddrPort) {
		if peer.Port() != 68 {
			return
		}
		r, err := DecodeRequest(p)
		if err != nil {
			return
		}
		select {
		case packets <- r:
		case <-ctx.Done():
		}
	}, nil)
	if err != nil {
		cancel()
		return nil, err
	}
	r := &Runtime{cancel: cancel, stopping: ctx.Done(), done: make(chan struct{}), apply: make(chan applyRequest), transport: t}
	r.publish(e, s, generation, true)
	go r.run(ctx, e, s, generation, link, probe, store, packets)
	return r, nil
}
func (r *Runtime) Done() <-chan struct{} { return r.done }
func (r *Runtime) Close(ctx context.Context) error {
	r.cancel()
	select {
	case <-r.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (r *Runtime) Apply(ctx context.Context, s Settings, g uint64) error {
	q := applyRequest{ctx, s.Clone(), g, make(chan error, 1)}
	select {
	case r.apply <- q:
	case <-r.stopping:
		return errors.New("dhcp: runtime stopping")
	case <-r.done:
		return errors.New("dhcp: runtime stopped")
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-q.result:
		return err
	case <-r.stopping:
		return errors.New("dhcp: runtime stopping")
	case <-r.done:
		return errors.New("dhcp: runtime stopped")
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (r *Runtime) Status() RuntimeStatus {
	r.mu.RLock()
	s := r.status
	r.mu.RUnlock()
	s.Transport = r.transport.Stats()
	return s
}
func (r *Runtime) Projection() Projection {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p := r.projection
	p.Settings = p.Settings.Clone()
	p.Leases = append([]Lease(nil), p.Leases...)
	return p
}

func (r *Runtime) View() *LeaseView { return r.view.Load() }
func (r *Runtime) Leases() []Lease {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]Lease(nil), r.leases...)
}

func (r *Runtime) Inspect() LeaseSnapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return LeaseSnapshot{r.status.Generation, r.leaseRevision, append([]Lease(nil), r.leases...)}
}
func (r *Runtime) publish(e *Engine, s Settings, g uint64, durable bool) {
	leases := e.Leases()
	pending := 0
	for _, l := range leases {
		if l.State == CommitPending {
			pending++
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.leaseRevision == 0 || r.status.Generation != g || !slices.Equal(r.leases, leases) {
		r.leaseRevision++
	}
	r.leases = leases
	r.status.Generation = g
	r.status.Capacity = s.Capacity()
	r.status.Held = len(leases)
	r.status.Pending = pending
	r.status.ClockSuspended = e.ClockSuspended()
	if r.status.Storage == "" {
		r.status.Storage = "healthy"
	}
	if durable {
		r.projection = Projection{g, true, s.Clone(), e.DurableLeases()}
		r.view.Store(&LeaseView{projection: r.projection})
	}
}
func (r *Runtime) run(ctx context.Context, e *Engine, s Settings, g uint64, link Link, probe ProbeFunc, store LeaseWriter, packets <-chan Request) {
	packetCtx, stopPackets := context.WithCancel(ctx)
	transportDone := make(chan error, 1)
	transportStopped := make(chan struct{})
	go func() { defer close(transportStopped); transportDone <- r.transport.Run(packetCtx) }()
	scheduler := NewProbeScheduler(probe)
	probeDone := make(chan struct{})
	go func() { defer close(probeDone); scheduler.Run(packetCtx) }()
	// Even a stuck fsync remains owned by this runtime. Close callers can time out,
	// but Done cannot close and replacement cannot start until the writer exits.
	defer func() {
		r.cancel()
		stopPackets()
		_ = link.Close()
		<-transportStopped
		<-probeDone
		_ = store.Close(context.Background())
		close(r.done)
	}()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	fail := func(err error, storage bool) {
		r.mu.Lock()
		r.status.Error = err.Error()
		if storage {
			r.status.Storage = "failed"
			if errors.Is(err, ErrStoreUncertain) {
				r.status.Storage = "uncertain"
			}
		}
		r.mu.Unlock()
	}
	var dispatch func(Outcome)
	dispatch = func(o Outcome) {
		if o.Mutation != nil {
			if err := store.Submit(ctx, *o.Mutation); err != nil {
				dispatch(e.CompleteCommit(CommitResult{Token: o.Mutation.Token, Err: err}))
			}
		}
		if o.Probe != nil && !scheduler.Submit(*o.Probe) {
			dispatch(e.CompleteProbe(ProbeResult{Token: o.Probe.Token, Err: errors.New("probe busy")}))
		}
		if o.Reply != nil && ctx.Err() == nil && store.Err() == nil {
			w, err := BuildReply(s, *o.Reply, link.MTU())
			if err == nil {
				err = link.Send(ctx, w)
			}
			if err != nil {
				fail(err, false)
			}
		}
	}
	var pending *applyRequest
	for {
		// Unknown writes have NO completion: observe the latch independently and
		// stop admission. Recovery is exclusively close/reopen, never rollback.
		if err := store.Err(); err != nil {
			fail(err, true)
			return
		}
		if ctx.Err() != nil {
			return
		}
		if pending != nil {
			err := pending.ctx.Err()
			if err == nil {
				err = e.Apply(pending.settings, pending.generation)
			}
			if !errors.Is(err, ErrMutationPending) {
				if err == nil {
					s = pending.settings
					g = pending.generation
					r.publish(e, s, g, true)
				}
				pending.result <- err
				pending = nil
			}
		}
		select {
		case <-ctx.Done():
			return
		case err := <-transportDone:
			if ctx.Err() == nil {
				fail(err, false)
			}
			return
		case q := <-r.apply:
			if pending != nil {
				q.result <- ErrMutationPending
			} else if !q.settings.Enabled {
				q.result <- errors.New("dhcp: use Close to disable runtime")
			} else {
				pending = &q
			}
		case request := <-packets:
			var outcome Outcome
			if pending == nil {
				outcome = e.Handle(request)
				dispatch(outcome)
			}
			if outcome.Mutation != nil || outcome.Probe != nil {
				r.publish(e, s, g, false)
			} else {
				// Ignored packets and retransmitted replies do not change visible
				// lease rows. Preserve the inspection snapshot instead of copying
				// and sorting thousands of rows for each excess identity.
				r.mu.Lock()
				r.status.ClockSuspended = e.ClockSuspended()
				r.mu.Unlock()
			}
		case result := <-scheduler.Results():
			dispatch(e.CompleteProbe(result))
			r.publish(e, s, g, false)
		case result, ok := <-store.Results():
			if !ok {
				if err := store.Err(); err != nil {
					fail(err, true)
				} else {
					fail(errors.New("lease writer stopped"), true)
				}
				return
			}
			dispatch(e.CompleteCommit(result))
			// Publish one committed projection for all currently available results.
			draining := true
			for draining {
				select {
				case result, ok := <-store.Results():
					if !ok {
						draining = false
					} else {
						dispatch(e.CompleteCommit(result))
					}
				default:
					draining = false
				}
			}
			r.publish(e, s, g, true)
		case <-ticker.C:
			held := len(e.byIP)
			mutations := e.Tick()
			for _, m := range mutations {
				dispatch(Outcome{Mutation: &m})
			}
			if len(mutations) != 0 || len(e.byIP) != held {
				r.publish(e, s, g, false)
			} else {
				r.mu.Lock()
				r.status.ClockSuspended = e.ClockSuspended()
				r.mu.Unlock()
			}
		}
	}
}
