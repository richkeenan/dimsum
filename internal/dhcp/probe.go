package dhcp

import (
	"context"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"
)

// ProbeFunc performs an ARP conflict check on the runtime's selected interface.
// It MUST honor cancellation/deadlines. Error or timeout is never a silent probe.
type ProbeFunc func(context.Context, netip.Addr) (conflict bool, err error)

// ProbeScheduler has two fixed workers, no waiting job queue, and two result
// slots. Submit never blocks. An unconsumed result holds its worker; overload
// cannot create additional work. Construction is inert; Run is single-use.
type ProbeScheduler struct {
	probe   ProbeFunc
	jobs    chan Probe
	results chan ProbeResult
	started atomic.Bool
}

func NewProbeScheduler(probe ProbeFunc) *ProbeScheduler {
	return &ProbeScheduler{probe: probe, jobs: make(chan Probe), results: make(chan ProbeResult, 2)}
}
func (s *ProbeScheduler) Submit(p Probe) bool {
	select {
	case s.jobs <- p:
		return true
	default:
		return false
	}
}
func (s *ProbeScheduler) Results() <-chan ProbeResult { return s.results }
func (s *ProbeScheduler) Run(ctx context.Context) {
	if !s.started.CompareAndSwap(false, true) {
		return
	}
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			for {
				select {
				case <-ctx.Done():
					return
				case p := <-s.jobs:
					deadline := time.Now().Add(500 * time.Millisecond)
					if p.Deadline.Before(deadline) {
						deadline = p.Deadline
					}
					work, cancel := context.WithDeadline(ctx, deadline)
					conflict, err := s.probe(work, p.Address)
					if work.Err() != nil {
						err = work.Err()
					}
					cancel()
					select {
					case s.results <- ProbeResult{Token: p.Token, Conflict: conflict, Err: err}:
					case <-ctx.Done():
						return
					}
				}
			}
		})
	}
	wg.Wait()
	close(s.results)
}
