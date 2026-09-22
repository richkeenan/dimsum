package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/richkeenan/dimsum/internal/dhcp"
)

type DHCPStatus struct {
	State             string             `json:"state"`
	DesiredGeneration uint64             `json:"desired_generation"`
	AppliedGeneration uint64             `json:"applied_generation"`
	PendingGeneration uint64             `json:"pending_generation,omitempty"`
	DesiredEnabled    bool               `json:"desired_enabled"`
	DesiredInterface  string             `json:"desired_interface"`
	DesiredServerIP   string             `json:"desired_server_ip"`
	AppliedEnabled    bool               `json:"applied_enabled"`
	Interface         string             `json:"interface"`
	ServerIP          string             `json:"server_ip"`
	LastError         string             `json:"last_error,omitempty"`
	Runtime           dhcp.RuntimeStatus `json:"runtime"`
}

// DHCPSupervisor owns the optional subsystem, including a closing writer that
// outlives a caller's timeout. Constructing/reconciling disabled settings opens
// no files/sockets and starts no timers/workers. The existing app watcher drives
// Reconcile; no DHCP watcher runs while disabled.
type DHCPSupervisor struct {
	gate              chan struct{}
	mu                sync.RWMutex
	closed            bool
	dir               string
	dns               []string
	status            DHCPStatus
	applied           dhcp.Settings
	runtime           *dhcp.Runtime
	closing           bool
	closingSettings   dhcp.Settings
	closingGeneration uint64
	attempt           uint64
	attemptErr        error
	openLink          func(dhcp.Settings) (dhcp.Link, dhcp.ProbeFunc, error)
	openStore         func(string) (dhcp.LeaseWriter, dhcp.LeaseRecovery, error)
}

func NewDHCPSupervisor(dataDir string, dns []string) *DHCPSupervisor {
	return &DHCPSupervisor{gate: make(chan struct{}, 1), dir: filepath.Join(dataDir, "dhcp"), dns: append([]string(nil), dns...), status: DHCPStatus{State: "disabled"}, openLink: dhcp.OpenSystemLink, openStore: func(path string) (dhcp.LeaseWriter, dhcp.LeaseRecovery, error) {
		return dhcp.OpenLeaseStore(path, dhcp.MaximumLeases, nil)
	}}
}
func (s *DHCPSupervisor) Status() DHCPStatus {
	s.mu.RLock()
	v, r := s.status, s.runtime
	s.mu.RUnlock()
	if r != nil {
		v.Runtime = r.Status()
		if v.Runtime.Error != "" {
			v.State = "degraded"
			v.LastError = v.Runtime.Error
		}
	}
	return v
}
func (s *DHCPSupervisor) Projection() dhcp.Projection {
	s.mu.RLock()
	r, enabled := s.runtime, s.status.AppliedEnabled && !s.closed && !s.closing
	s.mu.RUnlock()
	if r == nil || !enabled {
		return dhcp.Projection{}
	}
	return r.Projection()
}

func (s *DHCPSupervisor) View() *dhcp.LeaseView {
	s.mu.RLock()
	r, enabled := s.runtime, s.status.AppliedEnabled && !s.closed && !s.closing
	s.mu.RUnlock()
	if r == nil || !enabled {
		return nil
	}
	return r.View()
}
func (s *DHCPSupervisor) Leases() []dhcp.Lease {
	s.mu.RLock()
	r := s.runtime
	s.mu.RUnlock()
	if r == nil {
		return nil
	}
	return r.Leases()
}

func (s *DHCPSupervisor) Inspect() dhcp.LeaseSnapshot {
	s.mu.RLock()
	r := s.runtime
	s.mu.RUnlock()
	if r == nil {
		return dhcp.LeaseSnapshot{}
	}
	return r.Inspect()
}
func (s *DHCPSupervisor) Reconcile(ctx context.Context, next dhcp.Settings, g uint64) error {
	select {
	case s.gate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	s.mu.RLock()
	closed, inert := s.closed, !next.Enabled && s.runtime == nil
	s.mu.RUnlock()
	if closed {
		<-s.gate
		return errors.New("dhcp: supervisor closed")
	}
	if inert {
		defer func() { <-s.gate }()
		return s.reconcile(ctx, next, g)
	}
	// One owned preparation/reconciliation worker, only while enabled or closing.
	// A stuck syscall can outlive the caller, but retains the gate and resources:
	// no accumulating replacement workers and no blocked DNS/admin shutdown.
	result := make(chan error, 1)
	next = next.Clone()
	go func() { defer func() { <-s.gate }(); result <- s.reconcile(ctx, next, g) }()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *DHCPSupervisor) reconcile(ctx context.Context, next dhcp.Settings, g uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if g == 0 {
		return errors.New("dhcp: desired generation must be nonzero")
	}
	s.mu.Lock()
	if g < s.status.DesiredGeneration {
		s.mu.Unlock()
		return errors.New("dhcp: obsolete desired generation")
	}
	s.status.DesiredGeneration = g
	s.status.DesiredEnabled = next.Enabled
	s.status.DesiredInterface = next.Interface
	s.status.DesiredServerIP = next.ServerIP
	if s.status.AppliedGeneration != g {
		s.status.PendingGeneration = g
	}
	r, old, closing := s.runtime, s.applied, s.closing
	s.mu.Unlock()
	failed := func(err error) error {
		s.mu.Lock()
		s.status.LastError = err.Error()
		s.status.State = "error"
		if r != nil {
			s.status.State = "degraded"
		}
		s.mu.Unlock()
		s.attempt = g
		s.attemptErr = err
		return err
	}
	// A timed-out disable still irreversibly cancels its runtime. Finish that
	// owned close before validating/applying the newest desired generation.
	// This records the completed disabled boundary even if desired has advanced.
	if closing {
		if err := s.finishClosing(ctx); err != nil {
			return failed(err)
		}
		s.mu.RLock()
		r, old = s.runtime, s.applied
		s.mu.RUnlock()
	}
	if err := dhcp.ValidateTransition(old, next); err != nil {
		return failed(err)
	}
	if !next.Enabled {
		// Hide dynamic names immediately, even if a kernel fsync delays closing.
		s.mu.Lock()
		s.status.AppliedEnabled = false
		if r != nil {
			s.closing = true
			s.closingSettings = next.Clone()
			s.closingGeneration = g
		}
		s.mu.Unlock()
		if r != nil {
			if err := s.finishClosing(ctx); err != nil {
				return failed(err)
			}
		}
		return s.publishApplied(next, g)
	}
	if r != nil {
		if err := r.Err(); err != nil {
			return failed(errors.New(err.Error() + "; disable and re-enable to recover"))
		}
		if r.Status().Generation == g {
			// Completion may have raced the previous caller's deadline. The owner
			// is authoritative; do not re-apply an already installed generation.
			return s.publishApplied(next, g)
		}
		// The engine owns reconciliation and rejects pending writes/conflicts without
		// dropping old state. The app watcher can retry a temporarily pending switch.
		if err := r.Apply(ctx, next, g); err != nil {
			return failed(err)
		}
		return s.publishApplied(next, g)
	}
	if s.attempt == g {
		return s.attemptErr
	}
	s.mu.Lock()
	s.status.State = "starting"
	s.status.Runtime = dhcp.RuntimeStatus{Storage: "unopened", Capacity: next.Capacity()}
	s.mu.Unlock()
	if err := dhcp.ValidateDNS(next, s.dns); err != nil {
		return failed(err)
	}
	// Once preparation starts, the caller's deadline only stops its wait.
	// The gate keeps this work owned until it finishes; a healthy but slow open
	// must not become a permanently latched failure. Close fences publication
	// through s.closed even after the waiting caller has gone away.
	link, probe, err := s.openLink(next)
	if err != nil {
		return failed(err)
	}
	success := false
	defer func() {
		if !success {
			_ = link.Close()
		}
	}()
	s.mu.RLock()
	closed := s.closed
	s.mu.RUnlock()
	if closed {
		return failed(errors.New("dhcp: preparation canceled"))
	}
	if err = os.MkdirAll(filepath.Dir(s.dir), 0700); err != nil {
		return failed(err)
	}
	// Engine enforces configured capacity; store has the hard maximum so a live
	// capacity increase does not replace the writer or lose its token ordering.
	store, recovery, err := s.openStore(s.dir)
	if err != nil {
		s.mu.Lock()
		s.status.Runtime.Storage = "failed"
		s.mu.Unlock()
		return failed(err)
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = store.Close(context.Background())
		return failed(errors.New("dhcp: preparation canceled"))
	}
	runtime, err := dhcp.StartRuntime(context.Background(), next, g, link, probe, store, recovery)
	if err != nil {
		s.mu.Unlock()
		_ = store.Close(context.Background())
		return failed(err)
	}
	s.runtime = runtime
	s.mu.Unlock()
	success = true
	return s.publishApplied(next, g)
}

// publishApplied is the final owner-to-supervisor publication boundary. The
// reconciliation gate is held; lifecycle publication is serialized by mu.
func (s *DHCPSupervisor) publishApplied(next dhcp.Settings, g uint64) error {
	s.mu.Lock()
	// Close and successful publication share this lock. Once closed is set,
	// neither preparation nor a completed owner Apply can resurrect visibility.
	if s.closed {
		s.mu.Unlock()
		return errors.New("dhcp: supervisor closed before application publication")
	}
	s.applied = next.Clone()
	s.status.AppliedGeneration = g
	s.status.PendingGeneration = 0
	s.status.AppliedEnabled = next.Enabled
	s.status.Interface = next.Interface
	s.status.ServerIP = next.ServerIP
	s.status.LastError = ""
	s.status.State = "disabled"
	if next.Enabled {
		s.status.State = "running"
	} else {
		s.status.Runtime = dhcp.RuntimeStatus{Storage: "closed", Capacity: next.Capacity()}
	}
	s.mu.Unlock()
	s.attempt = g
	s.attemptErr = nil
	return nil
}

// The reconciliation gate is held throughout. Done/Close, not a caller's
// deadline, determines when another writer may recover the ownership directory.
func (s *DHCPSupervisor) finishClosing(ctx context.Context) error {
	s.mu.RLock()
	r := s.runtime
	s.mu.RUnlock()
	if r != nil {
		if err := r.Close(ctx); err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.runtime = nil
	s.closing = false
	s.applied = s.closingSettings.Clone()
	s.status.AppliedGeneration = s.closingGeneration
	s.status.AppliedEnabled = false
	s.status.Interface = s.applied.Interface
	s.status.ServerIP = s.applied.ServerIP
	s.status.State = "disabled"
	s.status.Runtime = dhcp.RuntimeStatus{Storage: "closed", Capacity: s.applied.Capacity()}
	s.mu.Unlock()
	s.attempt = 0
	s.attemptErr = nil
	return nil
}
func (s *DHCPSupervisor) Close(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	r := s.runtime
	s.status.AppliedEnabled = false
	s.status.State = "degraded"
	s.status.LastError = "dhcp: shutdown pending"
	s.mu.Unlock()
	if r != nil {
		if err := r.Close(ctx); err != nil {
			return err
		}
	}
	select {
	case s.gate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-s.gate }()
	s.mu.Lock()
	s.status.AppliedEnabled = false
	s.status.State = "disabled"
	s.status.LastError = ""
	s.runtime = nil
	s.mu.Unlock()
	return nil
}

func (s *Service) DHCPStatus() DHCPStatus {
	s.mu.Lock()
	d := s.dhcp
	s.mu.Unlock()
	if d == nil {
		return DHCPStatus{State: "disabled"}
	}
	return d.Status()
}
func (s *Service) DHCPProjection() dhcp.Projection {
	s.mu.Lock()
	d := s.dhcp
	s.mu.Unlock()
	if d == nil {
		return dhcp.Projection{}
	}
	return d.Projection()
}

func (s *Service) DHCPView() *dhcp.LeaseView {
	s.mu.Lock()
	d := s.dhcp
	s.mu.Unlock()
	if d == nil {
		return nil
	}
	return d.View()
}
func (s *Service) DHCPLeases() []dhcp.Lease {
	s.mu.Lock()
	d := s.dhcp
	s.mu.Unlock()
	if d == nil {
		return nil
	}
	return d.Leases()
}

func (s *Service) DHCPInspect() dhcp.LeaseSnapshot {
	s.mu.Lock()
	d := s.dhcp
	s.mu.Unlock()
	if d == nil {
		return dhcp.LeaseSnapshot{}
	}
	return d.Inspect()
}
