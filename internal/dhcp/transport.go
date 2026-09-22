// Package dhcp provides the bounded packet runtime for the optional DHCP service.
// Constructing a transport does not open sockets or start workers.
package dhcp

import (
	"context"
	"errors"
	"net/netip"
	"sync/atomic"
	"time"

	"github.com/insomniacslk/dhcp/dhcpv4"
	"golang.org/x/time/rate"
)

const (
	MaxDatagram      = 4096
	QueueSize        = 32
	PacketsPerSecond = 200
	AdmissionBurst   = 64
)

// Receiver must report truncation even if the copied prefix is a valid packet.
// Close must unblock Receive, and may race it. Transport owns the receiver.
type Receiver interface {
	Receive([]byte) (n int, peer netip.AddrPort, truncated bool, err error)
	Close() error
}

// Handler runs serially. It must return promptly when ctx is canceled, and must
// not retain the packet (including slices/options) after returning. Future lease
// processing copies only bounded identity/hostname fields into owned state.
type Handler func(ctx context.Context, packet *dhcpv4.DHCPv4, peer netip.AddrPort)

type Counters struct{ Received, Admitted, Oversized, RateLimited, QueueFull, Malformed uint64 }
type counters struct{ received, admitted, oversized, rateLimited, queueFull, malformed atomic.Uint64 }
type datagram struct {
	wire [MaxDatagram]byte
	n    int
	peer netip.AddrPort
}

type Transport struct {
	conn    Receiver
	handler Handler
	now     func() time.Time
	started atomic.Bool
	counts  counters
}

func NewTransport(conn Receiver, handler Handler, now func() time.Time) (*Transport, error) {
	if conn == nil || handler == nil {
		return nil, errors.New("DHCP receiver and handler are required")
	}
	if now == nil {
		now = time.Now
	}
	return &Transport{conn: conn, handler: handler, now: now}, nil
}

// Stats is safe during Run; fields are independent monotonically increasing
// counters, not a transactional snapshot.
func (t *Transport) Stats() Counters {
	c := &t.counts
	return Counters{c.received.Load(), c.admitted.Load(), c.oversized.Load(), c.rateLimited.Load(), c.queueFull.Load(), c.malformed.Load()}
}

// Run is single-use: one receiver, one serial decoder/owner, one cancellation
// watcher. A fixed 33-slot pool allows 32 queued packets plus one being handled.
// The extra receive byte detects oversize even on silently truncating readers;
// the explicit truncation flag also covers datagrams truncated below that size.
func (t *Transport) Run(parent context.Context) error {
	if !t.started.CompareAndSwap(false, true) {
		return errors.New("DHCP transport already run")
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	free := make(chan *datagram, QueueSize+1)
	queue := make(chan *datagram, QueueSize)
	slots := make([]datagram, QueueSize+1)
	for i := range slots {
		free <- &slots[i]
	}
	stopped := make(chan struct{})
	go func() { defer close(stopped); <-ctx.Done(); _ = t.conn.Close() }()
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		for {
			select {
			case <-ctx.Done():
				return
			case d := <-queue:
				if ctx.Err() != nil {
					return
				}
				p, err := dhcpv4.FromBytes(d.wire[:d.n])
				if err != nil {
					t.counts.malformed.Add(1)
				} else {
					t.handler(ctx, p, d.peer)
				}
				free <- d
			}
		}
	}()
	defer func() { cancel(); <-stopped; <-workerDone }()
	limiter := rate.NewLimiter(PacketsPerSecond, AdmissionBurst)
	var receive [MaxDatagram + 1]byte
	for {
		n, peer, truncated, err := t.conn.Receive(receive[:])
		if err != nil {
			if parent.Err() != nil {
				return parent.Err()
			}
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		t.counts.received.Add(1)
		if truncated || n > MaxDatagram || n < 0 {
			t.counts.oversized.Add(1)
			continue
		}
		if !limiter.AllowN(t.now(), 1) {
			t.counts.rateLimited.Add(1)
			continue
		}
		var d *datagram
		select {
		case d = <-free:
		default:
			t.counts.queueFull.Add(1)
			continue
		}
		d.n, d.peer = n, peer
		copy(d.wire[:], receive[:n])
		select {
		case queue <- d:
			t.counts.admitted.Add(1)
		default:
			free <- d
			t.counts.queueFull.Add(1)
		}
	}
}
