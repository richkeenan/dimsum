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

// ProbeScheduler has two fixed workers, one reserved handoff slot per worker,
// no additional waiting backlog, and two result slots. Submit never blocks or
// depends on a worker already being scheduled. Construction is inert; Run is
// single-use. A blocked result delivery holds its worker slot.
type ProbeScheduler struct {
	probe   ProbeFunc
	workers [2]chan Probe
	idle    chan chan Probe
	results chan ProbeResult
	started atomic.Bool
	stopped atomic.Bool
}

func NewProbeScheduler(probe ProbeFunc) *ProbeScheduler {
	s := &ProbeScheduler{probe: probe, idle: make(chan chan Probe, 2), results: make(chan ProbeResult, 2)}
	for i := range s.workers {
		s.workers[i] = make(chan Probe, 1)
		s.idle <- s.workers[i]
	}
	return s
}
func (s *ProbeScheduler) Submit(p Probe) bool {
	if s.stopped.Load() {
		return false
	}
	select {
	case worker := <-s.idle:
		worker <- p
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
	for _, worker := range s.workers {
		wg.Go(func() {
			for {
				select {
				case <-ctx.Done():
					return
				case p := <-worker:
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
					s.idle <- worker
				}
			}
		})
	}
	wg.Wait()
	s.stopped.Store(true)
	close(s.results)
}
