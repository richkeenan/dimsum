package resolve

import (
	"context"
	"net"
	"sync"

	"github.com/richkeenan/dimsum/internal/config"
	"github.com/richkeenan/dimsum/internal/upstream"
)

type upstreamLease struct {
	client *upstream.Client
	stop   func() bool
}
type upstreams struct {
	mu         sync.Mutex
	current    *upstreamLease
	generation uint64
	routes     map[context.Context]*upstreamLease
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

// exchange uses the selected route in the captured generation. At most 64 live
// route transports are retained, shared across devices and policy-only reloads.
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
	lifetime := snapshot.RouteContext(route)
	if err := lifetime.Err(); err != nil {
		return result, err
	}
	m := &p.upstreams
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return upstream.ExchangeResult{}, net.ErrClosed
	}
	entry := m.routes[lifetime]
	if entry == nil {
		// Existing routes need no admission scan. Before creating a new one,
		// prune synchronously: retirement callbacks may be waiting on this lock.
		for key, old := range m.routes {
			if key.Err() != nil {
				old.stop()
				old.client.Close()
				delete(m.routes, key)
				if m.current == old {
					m.current = nil
				}
			}
		}
		if err := lifetime.Err(); err != nil {
			m.mu.Unlock()
			return result, err
		}
		if len(m.routes) >= 64 {
			m.mu.Unlock()
			return result, upstream.ErrRoute
		}
		options, ok := snapshot.ClientPolicies().RouteOptions(route)
		if !ok {
			m.mu.Unlock()
			return result, upstream.ErrRoute
		}
		client, err := upstream.New(options)
		if err != nil {
			m.mu.Unlock()
			return result, err
		}
		entry = &upstreamLease{client: client}
		if m.routes == nil {
			m.routes = make(map[context.Context]*upstreamLease)
		}
		m.routes[lifetime] = entry
		entry.stop = context.AfterFunc(lifetime, func() {
			m.mu.Lock()
			defer m.mu.Unlock()
			client.Close()
			delete(m.routes, lifetime)
			if m.current == entry {
				m.current = nil
			}
		})
	}
	if route == upstream.DefaultRoute && snapshot.Generation() >= m.generation {
		m.current = entry
		m.generation = snapshot.Generation()
	}
	m.mu.Unlock()
	result, err = entry.client.Exchange(ctx, wire, out)
	result.Route = route
	// History keys are generation-local. Each route has at most 16 primary and
	// 16 fallback endpoints; preserve legacy network IDs while avoiding collisions.
	if result.EndpointID != 0 {
		result.EndpointID += uint32(route-1) * 32
	}
	return result, err
}

// Close cancels and joins shared workers, then closes all route transports.
func (p *Pipeline) Close() error {
	p.cache.flights.close()
	m := &p.upstreams
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	for key, entry := range m.routes {
		entry.stop()
		entry.client.Close()
		delete(m.routes, key)
	}
	m.current = nil
	if p.upstream != nil {
		return p.upstream.Close()
	}
	return nil
}
