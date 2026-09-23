package resolve

import (
	"context"
	"errors"
	"net"
	"sync"

	"github.com/richkeenan/dimsum/internal/dnscache"
	"github.com/richkeenan/dimsum/internal/upstream"
)

const maxFlights = 128
const maxWaiters = 64
const maxRefresh = 16

var errFlightFull = errors.New("resolve: shared resolution capacity exhausted")

type flight struct {
	done     chan struct{}
	cancel   context.CancelFunc
	waiters  int
	refresh  bool
	wire     []byte
	err      error
	metadata *upstream.ExchangeResult
}

// Work owns request/response bytes. Waiters only read a completed immutable
// response; no transport or cache arena storage is retained by this table.
type flights struct {
	mu        sync.Mutex
	active    map[dnscache.Key]*flight
	refreshes int
	workers   int
	closed    bool
	wg        sync.WaitGroup
}

func (g *flights) join(k dnscache.Key, refresh bool, work func(context.Context) ([]byte, error)) (*flight, error) {
	f, _, err := g.joinStatus(k, refresh, work, nil, nil)
	return f, err
}

// prepare runs synchronously only for an admitted leader, before its worker can
// start. It must be bounded and must not call back into flights.
func (g *flights) joinStatus(k dnscache.Key, refresh bool, work func(context.Context) ([]byte, error), metadata *upstream.ExchangeResult, prepare func()) (*flight, bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return nil, false, net.ErrClosed
	}
	if f := g.active[k]; f != nil {
		if !refresh {
			if f.waiters >= maxWaiters {
				return nil, false, errFlightFull
			}
			f.waiters++
		}
		return f, true, nil
	}
	if g.workers >= maxFlights || (refresh && g.refreshes >= maxRefresh) {
		return nil, false, errFlightFull
	}
	if g.active == nil {
		g.active = make(map[dnscache.Key]*flight)
	}
	if prepare != nil {
		prepare()
	}
	// The upstream client enforces its configured bounded total timeout. This
	// context belongs to the shared work, not to any individual client deadline.
	ctx, cancel := context.WithCancel(context.Background())
	f := &flight{done: make(chan struct{}), cancel: cancel, refresh: refresh, metadata: metadata}
	if refresh {
		g.refreshes++
	} else {
		f.waiters = 1
	}
	g.active[k] = f
	g.workers++
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		defer cancel()
		f.wire, f.err = work(ctx)
		g.mu.Lock()
		if g.active[k] == f {
			delete(g.active, k)
		}
		g.workers--
		if refresh {
			g.refreshes--
		}
		close(f.done)
		g.mu.Unlock()
	}()
	return f, false, nil
}

func (g *flights) wait(ctx context.Context, k dnscache.Key, f *flight) ([]byte, error) {
	defer func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		f.waiters--
		if f.waiters == 0 && !f.refresh {
			f.cancel()
			// A live request must never attach to abandoned work. Its worker
			// remains charged until it exits, even after detaching from the key.
			if g.active[k] == f {
				delete(g.active, k)
			}
		}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-f.done:
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return f.wire, f.err
	}
}

func (g *flights) close() {
	g.mu.Lock()
	g.closed = true
	for _, f := range g.active {
		f.cancel()
	}
	g.mu.Unlock()
	g.wg.Wait()
}
