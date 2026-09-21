// Package testutil contains test-only infrastructure. Production packages must
// not import it.
package testutil

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

// Clock advances only when the test asks. No wall-clock sleeps are needed for
// upstream response latency. Its timers are cancellable on fixture shutdown.
type Clock struct {
	mu     sync.Mutex
	now    time.Time
	timers map[*timer]struct{}
}
type timer struct {
	at   time.Time
	done chan struct{}
}

func NewClock(now time.Time) *Clock { return &Clock{now: now, timers: make(map[*timer]struct{})} }
func (c *Clock) Now() time.Time     { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *Clock) Advance(d time.Duration) {
	if d < 0 {
		panic("test clock cannot move backwards")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	for t := range c.timers {
		if !t.at.After(c.now) {
			close(t.done)
			delete(c.timers, t)
		}
	}
}
func (c *Clock) Pending() int { c.mu.Lock(); defer c.mu.Unlock(); return len(c.timers) }
func (c *Clock) after(d time.Duration) (<-chan struct{}, func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &timer{at: c.now.Add(d), done: make(chan struct{})}
	if d <= 0 {
		close(t.done)
	} else {
		c.timers[t] = struct{}{}
	}
	return t.done, func() { c.mu.Lock(); delete(c.timers, t); c.mu.Unlock() }
}

type Request struct {
	Network string
	Wire    []byte
	At      time.Time
}
type Response struct {
	Wire  []byte
	Delay time.Duration
	Drop  bool
}
type Upstream struct {
	clock    *Clock
	handler  func(Request) Response
	tcp      net.Listener
	udp      net.PacketConn
	ctx      context.Context
	cancel   context.CancelFunc
	requests chan Request
	wg       sync.WaitGroup
	mu       sync.Mutex
	conn     net.Conn
	closed   bool
	err      error
	once     sync.Once
}

// NewUpstream binds loopback UDP and TCP on the same ephemeral test port.
// The handler can run concurrently for UDP/TCP and must return promptly.
// The fixture serializes requests within each transport, bounding goroutines.
func NewUpstream(clock *Clock, handler func(Request) Response) (*Upstream, error) {
	if clock == nil || handler == nil {
		return nil, fmt.Errorf("clock and handler required")
	}
	tcp, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	udp, err := net.ListenPacket("udp", tcp.Addr().String())
	if err != nil {
		tcp.Close()
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	u := &Upstream{clock: clock, handler: handler, tcp: tcp, udp: udp, ctx: ctx, cancel: cancel, requests: make(chan Request, 64)}
	u.wg.Add(2)
	go u.serveUDP()
	go u.serveTCP()
	return u, nil
}
func (u *Upstream) Address() string { return u.tcp.Addr().String() }

// Receiving a request guarantees its delay timer has been registered, so a test
// may immediately Advance. Consume notifications to avoid filling the queue.
func (u *Upstream) Requests() <-chan Request { return u.requests }

// Err reports an unexpected fixture UDP-write failure. Tests can inspect it
// while the fixture is running; Close also returns it after workers stop.
func (u *Upstream) Err() error {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.err
}

func (u *Upstream) respond(network string, wire []byte) (Response, bool) {
	r := Request{Network: network, Wire: append([]byte(nil), wire...), At: u.clock.Now()}
	response := u.handler(r)
	tick, stop := u.clock.after(response.Delay)
	defer stop()
	select {
	case u.requests <- r:
	case <-u.ctx.Done():
		return Response{}, false
	}
	select {
	case <-tick:
		return response, true
	case <-u.ctx.Done():
		return Response{}, false
	}
}
func (u *Upstream) serveUDP() {
	defer u.wg.Done()
	buf := make([]byte, 65535)
	for {
		n, peer, err := u.udp.ReadFrom(buf)
		if err != nil {
			return
		}
		response, ok := u.respond("udp", buf[:n])
		if !ok {
			return
		}
		if !response.Drop {
			if _, err := u.udp.WriteTo(response.Wire, peer); err != nil {
				if u.ctx.Err() != nil && errors.Is(err, net.ErrClosed) {
					return
				}
				u.mu.Lock()
				u.err = fmt.Errorf("fixture UDP response: %w", err)
				u.mu.Unlock()
				return
			}
		}
	}
}
func (u *Upstream) serveTCP() {
	defer u.wg.Done()
	for {
		c, err := u.tcp.Accept()
		if err != nil {
			return
		}
		u.mu.Lock()
		if u.closed {
			u.mu.Unlock()
			c.Close()
			return
		}
		u.conn = c
		u.mu.Unlock()
		u.serveConn(c)
		c.Close()
		u.mu.Lock()
		u.conn = nil
		u.mu.Unlock()
	}
}
func (u *Upstream) serveConn(c net.Conn) {
	for {
		var size [2]byte
		if _, err := io.ReadFull(c, size[:]); err != nil {
			return
		}
		wire := make([]byte, int(binary.BigEndian.Uint16(size[:])))
		if _, err := io.ReadFull(c, wire); err != nil {
			return
		}
		response, ok := u.respond("tcp", wire)
		if !ok {
			return
		}
		if response.Drop {
			continue
		}
		if len(response.Wire) > 65535 {
			return
		}
		binary.BigEndian.PutUint16(size[:], uint16(len(response.Wire)))
		if _, err := io.Copy(c, bytes.NewReader(append(size[:], response.Wire...))); err != nil {
			return
		}
	}
}

func (u *Upstream) Close() error {
	u.once.Do(func() {
		u.cancel()
		u.tcp.Close()
		u.udp.Close()
		u.mu.Lock()
		u.closed = true
		if u.conn != nil {
			u.conn.Close()
		}
		u.mu.Unlock()
	})
	u.wg.Wait()
	return u.Err()
}
