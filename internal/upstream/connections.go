package upstream

import (
	"context"
	"net"
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
	idle   map[Endpoint]*idleConnection
	all    map[net.Conn]struct{}
}

// HTTP owns its lease/multiplexing policy. Track its underlying sockets as well
// so Close can terminate active HTTP/2 connections, not just currently idle ones.
type trackedConnection struct {
	net.Conn
	owner *connections
	once  sync.Once
}

func (c *trackedConnection) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { c.owner.mu.Lock(); delete(c.owner.all, c); c.owner.mu.Unlock() })
	return err
}
func (p *connections) track(conn net.Conn) (net.Conn, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		conn.Close()
		return nil, net.ErrClosed
	}
	if p.all == nil {
		p.all = make(map[net.Conn]struct{})
		p.idle = make(map[Endpoint]*idleConnection)
	}
	tracked := &trackedConnection{Conn: conn, owner: p}
	p.all[tracked] = struct{}{}
	return tracked, nil
}

func (p *connections) isClosed() bool { p.mu.Lock(); defer p.mu.Unlock(); return p.closed }
func (p *connections) take(ctx context.Context, endpoint Endpoint, dial func(context.Context, Endpoint) (net.Conn, error), fresh bool) (net.Conn, bool, error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, false, net.ErrClosed
	}
	if v := p.idle[endpoint]; v != nil && !fresh {
		delete(p.idle, endpoint)
		v.timer.Stop()
		p.mu.Unlock()
		return v.conn, true, nil
	}
	p.mu.Unlock()
	conn, err := dial(ctx, endpoint)
	if err != nil {
		return nil, false, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		conn.Close()
		return nil, false, net.ErrClosed
	}
	if p.all == nil {
		p.all = make(map[net.Conn]struct{})
		p.idle = make(map[Endpoint]*idleConnection)
	}
	p.all[conn] = struct{}{}
	return conn, false, nil
}
func (p *connections) put(endpoint Endpoint, conn net.Conn, healthy bool) {
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

// Close cancels every exchange and retires active and idle transport sockets.
func (c *Client) Close() error {
	c.cancel()
	c.httpMu.Lock()
	for _, client := range c.httpClients {
		client.CloseIdleConnections()
	}
	c.httpMu.Unlock()
	p := &c.connections
	p.mu.Lock()
	p.closed = true
	for _, v := range p.idle {
		v.timer.Stop()
	}
	all := p.all
	p.idle = nil
	p.all = nil
	p.mu.Unlock()
	for conn := range all {
		conn.Close()
	}
	return nil
}
