package resolve

import (
	"context"
	"net"
	"reflect"
	"sync"

	"github.com/richkeenan/dimsum/internal/config"
	"github.com/richkeenan/dimsum/internal/upstream"
)

type upstreamLease struct {
	client   *upstream.Client
	snapshot *config.Snapshot
	options  upstream.Options
	refs     int
	retired  bool
}
type upstreams struct {
	mu         sync.Mutex
	current    *upstreamLease
	generation uint64
	closed     bool
}

// exchange uses the same snapshot as policy/local resolution. Retired clients
// survive only while requests hold leases; their idle sockets are then closed.
func (p *Pipeline) exchange(ctx context.Context, snapshot *config.Snapshot, route upstream.RouteKey, wire, out []byte) (upstream.ExchangeResult, error) {
	if snapshot == nil {
		return p.upstream.ExchangeRoute(ctx, route, wire, out)
	}
	m := &p.upstreams
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return upstream.ExchangeResult{}, net.ErrClosed
	}
	entry := m.current
	if entry == nil || entry.snapshot != snapshot {
		options := snapshot.UpstreamOptions()
		if entry == nil || !reflect.DeepEqual(entry.options, options) {
			client, err := upstream.New(options)
			if err != nil {
				m.mu.Unlock()
				return upstream.ExchangeResult{}, err
			}
			entry = &upstreamLease{client: client, snapshot: snapshot, options: options}
			if snapshot.Generation() >= m.generation {
				if old := m.current; old != nil {
					old.retired = true
					if old.refs == 0 {
						old.client.Close()
					}
				}
				m.current = entry
			} else {
				entry.retired = true
			}
		}
		if snapshot.Generation() >= m.generation {
			m.generation = snapshot.Generation()
			entry.snapshot = snapshot
		}
	}
	entry.refs++
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		entry.refs--
		if entry.retired && entry.refs == 0 {
			entry.client.Close()
		}
	}()
	return entry.client.ExchangeRoute(ctx, route, wire, out)
}

// Close is called after transport workers have stopped; active leases still
// release normally if an owner closes earlier.
func (p *Pipeline) Close() error {
	m := &p.upstreams
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	if m.current != nil {
		m.current.retired = true
		if m.current.refs == 0 {
			m.current.client.Close()
		}
		m.current = nil
	}
	if p.upstream != nil {
		return p.upstream.Close()
	}
	return nil
}
