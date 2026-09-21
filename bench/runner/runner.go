// Package runner provides a bounded open-loop driver for local resolver experiments.
package runner

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/richkeenan/dimsum/bench/workload"
)

type Outcome string

const (
	NotOffered Outcome = "not-offered"
	Answered   Outcome = "answered"
	Failed     Outcome = "failed"
	Timeout    Outcome = "timeout"
	Canceled   Outcome = "canceled"
	Overflow   Outcome = "overflow"
)

// Handler must honor context cancellation. Factory runs once per worker before
// the clock starts, allowing worker-owned request/response buffers. A nil error
// means a validated answer, not necessarily DNS NOERROR.
type Handler func(context.Context, workload.Query) error
type Factory func() Handler

type Options struct {
	Workers int
	Queue   int
	Timeout time.Duration // measured from scheduled arrival, including queue delay
}

type Sample struct {
	Outcome   Outcome
	Scheduled time.Duration
	Started   time.Duration
	Finished  time.Duration
	Latency   time.Duration // completion minus scheduled arrival
	Service   time.Duration // handler completion minus handler start; zero if never called
}

type Report struct {
	Planned int
	Offered int
	Counts  map[Outcome]int
	Samples []Sample      // index order, at most workload.MaxQueries
	Elapsed time.Duration // includes drain
}

// Run never waits for queue capacity and never rebases arrivals on completions.
// If scheduling falls behind, overdue arrivals are still offered against their
// original timestamps. Cancellation stops further offers; remaining entries are
// explicitly NotOffered. Runtime is schedule + timeout for cooperative handlers.
func Run(ctx context.Context, w workload.Workload, o Options, factory Factory) (Report, error) {
	if _, err := workload.New(w.Spec()); err != nil {
		return Report{}, err
	}
	if o.Workers < 1 || o.Workers > 256 || o.Queue < 0 || o.Queue > workload.MaxQueries || o.Timeout <= 0 || o.Timeout > time.Minute || factory == nil {
		return Report{}, fmt.Errorf("invalid runner limits or factory")
	}
	handlers := make([]Handler, o.Workers)
	for i := range handlers {
		handlers[i] = factory()
		if handlers[i] == nil {
			return Report{}, fmt.Errorf("nil worker handler")
		}
	}
	r := Report{Planned: w.Spec().Count, Samples: make([]Sample, w.Spec().Count), Counts: make(map[Outcome]int)}
	for i := range r.Samples {
		r.Samples[i] = Sample{Outcome: NotOffered, Scheduled: w.At(i).Offset}
	}
	jobs := make(chan workload.Query, o.Queue)
	var workers sync.WaitGroup
	start := time.Now()
	for _, handler := range handlers {
		workers.Go(func() {
			for q := range jobs {
				s := &r.Samples[q.Index]
				requestCtx, cancel := context.WithDeadline(ctx, start.Add(q.Offset+o.Timeout))
				err := requestCtx.Err()
				if err == nil {
					s.Started = time.Since(start)
					err = handler(requestCtx, q)
					s.Finished = time.Since(start)
					s.Service = s.Finished - s.Started
					if requestCtx.Err() != nil {
						err = requestCtx.Err()
					}
				} else {
					s.Finished = time.Since(start)
				}
				cancel()
				s.Latency = s.Finished - q.Offset
				s.Outcome = Answered
				switch {
				case errors.Is(err, context.Canceled):
					s.Outcome = Canceled
				case errors.Is(err, context.DeadlineExceeded), s.Finished >= q.Offset+o.Timeout:
					s.Outcome = Timeout
				case err != nil:
					s.Outcome = Failed
				}
			}
		})
	}
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()
offer:
	for i := range r.Planned {
		q := w.At(i)
		if wait := time.Until(start.Add(q.Offset)); wait > 0 {
			timer.Reset(wait)
			select {
			case <-timer.C:
			case <-ctx.Done():
				break offer
			}
		}
		if ctx.Err() != nil {
			break
		}
		r.Offered++
		select {
		case jobs <- q:
		default:
			r.Samples[i].Outcome = Overflow
		}
	}
	close(jobs)
	workers.Wait()
	r.Elapsed = time.Since(start)
	for _, s := range r.Samples {
		r.Counts[s.Outcome]++
	}
	return r, ctx.Err()
}
