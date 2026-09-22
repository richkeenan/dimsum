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
	stop     func() bool
}
type upstreams struct {
	mu         sync.Mutex
	current    *upstreamLease
	generation uint64
	closed     bool
}

type backgroundExchangeKey struct{}

func (p *Pipeline) UpstreamHealth() []upstream.Health {
	p.upstreams.mu.Lock()
	defer p.upstreams.mu.Unlock()
	if p.upstreams.current != nil {
		return p.upstreams.current.client.Health()
	}
	if p.upstream != nil {
		return p.upstream.Health()
	}
	return nil
}

// exchange uses the same snapshot as policy/local resolution. Changed transport
// settings retire the previous client and cancel its active work immediately.
func (p *Pipeline) exchange(ctx context.Context, snapshot *config.Snapshot, route upstream.RouteKey, wire, out []byte) (result upstream.ExchangeResult, err error) {
	if p.observeExchange != nil {
		defer func() {
			result.Background, _ = ctx.Value(backgroundExchangeKey{}).(bool)
			p.observeExchange(result, err)
		}()
	}
	if snapshot == nil {
		return p.upstream.ExchangeRoute(ctx, route, wire, out)
	}
	if err := snapshot.UpstreamContext().Err(); err != nil {
		return result, err
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
			entry.stop = context.AfterFunc(snapshot.UpstreamContext(), func() { client.Close() })
			if snapshot.Generation() >= m.generation {
				if old := m.current; old != nil {
					old.retired = true
					old.stop()
					old.client.Close()
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
			entry.stop()
			entry.client.Close()
		}
	}()
	return entry.client.ExchangeRoute(ctx, route, wire, out)
}

// Close is called after transport workers have stopped; active leases still
// release normally if an owner closes earlier.
func (p *Pipeline) Close() error {
	p.cache.flights.close()
	m := &p.upstreams
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	if m.current != nil {
		m.current.retired = true
		m.current.stop()
		m.current.client.Close()
		m.current = nil
	}
	if p.upstream != nil {
		return p.upstream.Close()
	}
	return nil
}
