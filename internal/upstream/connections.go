package upstream

import (
	"context"
	"net"
	"net/netip"
	"sync"
	"time"
)

// Exclusive TCP leases need no wire multiplexing. IDs remain globally random
// and quarantined; a failed read/write/validation always retires the socket.
// UDP intentionally keeps a fresh random source port per attempt: sharing a
// single persistent UDP socket would sacrifice source-port entropy.
type idleConnection struct {
	conn  net.Conn
	timer *time.Timer
}
type connections struct {
	mu     sync.Mutex
	closed bool
	idle   map[netip.AddrPort]*idleConnection
	all    map[net.Conn]struct{}
}

func (p *connections) isClosed() bool { p.mu.Lock(); defer p.mu.Unlock(); return p.closed }
func (p *connections) take(ctx context.Context, endpoint netip.AddrPort) (net.Conn, error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, net.ErrClosed
	}
	if v := p.idle[endpoint]; v != nil {
		delete(p.idle, endpoint)
		v.timer.Stop()
		p.mu.Unlock()
		return v.conn, nil
	}
	p.mu.Unlock()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", endpoint.String())
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		conn.Close()
		return nil, net.ErrClosed
	}
	if p.all == nil {
		p.all = make(map[net.Conn]struct{})
		p.idle = make(map[netip.AddrPort]*idleConnection)
	}
	p.all[conn] = struct{}{}
	return conn, nil
}
func (p *connections) put(endpoint netip.AddrPort, conn net.Conn, healthy bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || !healthy || p.idle[endpoint] != nil {
		delete(p.all, conn)
		conn.Close()
		return
	}
	v := &idleConnection{conn: conn}
	p.idle[endpoint] = v
	v.timer = time.AfterFunc(30*time.Second, func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.idle[endpoint] == v {
			delete(p.idle, endpoint)
			delete(p.all, conn)
			conn.Close()
		}
	})
}

// Close retires active and idle TCP sockets. In-flight UDP work retains its
// request deadline; owners should cancel request contexts before shutdown.
func (c *Client) Close() error {
	p := &c.connections
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	for _, v := range p.idle {
		v.timer.Stop()
	}
	for conn := range p.all {
		conn.Close()
	}
	p.idle = nil
	p.all = nil
	return nil
}
