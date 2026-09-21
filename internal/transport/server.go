package transport

import (
	"context"
	"encoding/binary"
	"errors"
	"net/netip"
	"sync/atomic"
	"time"

	"github.com/richkeenan/dimsum/internal/dnswire"
)

// Request borrows immutable wire storage until Resolve returns. Message contains
// copied names and borrowed original question/EDNS options. Async consumers must
// copy. Deadline includes handler work; implementations MUST honor cancellation.
type Request struct {
	Wire    []byte
	Message dnswire.Message
	Peer    netip.AddrPort
	TCP     bool
}

// Handler writes a complete client-specific DNS reply into out and returns its
// length. It must not retain either buffer or spawn unbounded work. Resolver owns
// upstream authenticity, RD=0 miss policy, AD trust, and option handling.
type Handler interface {
	Resolve(context.Context, *Request, []byte) (int, error)
}
type HandlerFunc func(context.Context, *Request, []byte) (int, error)

func (f HandlerFunc) Resolve(c context.Context, r *Request, b []byte) (int, error) { return f(c, r, b) }

type Options struct {
	SmallSlots, LargeSlots, Workers, MaxConnections, UDPSize int
	ReadTimeout, WriteTimeout, RequestTimeout                time.Duration
}
type counters struct{ slotDrops, largeReceived, largeDrops, connectionDrops, connections, invalid, writeErrors atomic.Uint64 }
type Stats struct{ SlotDrops, LargeReceived, LargeDrops, ConnectionDrops, Connections, Invalid, WriteErrors uint64 }
type Server struct {
	opts        Options
	handler     Handler
	pool        *slots
	connections chan struct{}
	stats       counters
}

func New(o Options, h Handler) (*Server, error) {
	if h == nil {
		return nil, errors.New("transport: resolver handler required")
	}
	if o.SmallSlots < 0 || o.LargeSlots < 0 || o.Workers < 0 || o.MaxConnections < 0 || o.UDPSize < 0 || o.ReadTimeout < 0 || o.WriteTimeout < 0 || o.RequestTimeout < 0 {
		return nil, errors.New("transport: negative limit")
	}
	if o.SmallSlots == 0 {
		o.SmallSlots = 4096
	}
	if o.LargeSlots == 0 {
		o.LargeSlots = 16
	}
	if o.Workers == 0 {
		o.Workers = 4
	}
	if o.MaxConnections == 0 {
		o.MaxConnections = 128
	}
	if o.UDPSize == 0 {
		o.UDPSize = 1232
	}
	if o.UDPSize < 512 || o.UDPSize > 65535 {
		return nil, errors.New("transport: invalid UDP cap")
	}
	if o.ReadTimeout == 0 {
		o.ReadTimeout = 5 * time.Second
	}
	if o.WriteTimeout == 0 {
		o.WriteTimeout = time.Second
	}
	if o.RequestTimeout == 0 {
		o.RequestTimeout = 2 * time.Second
	}
	return &Server{opts: o, handler: h, pool: newSlots(o.SmallSlots, o.LargeSlots), connections: make(chan struct{}, o.MaxConnections)}, nil
}

// StorageBytes counts slot payloads only; UDP adds 65535 scratch bytes and one
// 65535 output per worker/listener; each TCP connection adds one 65535 output.
func (s *Server) StorageBytes() int { return s.pool.bytes() }
func (s *Server) Stats() Stats {
	return Stats{s.stats.slotDrops.Load(), s.stats.largeReceived.Load(), s.stats.largeDrops.Load(), s.stats.connectionDrops.Load(), s.stats.connections.Load(), s.stats.invalid.Load(), s.stats.writeErrors.Load()}
}
func (s *Server) acquire(n int) *RequestSlot {
	if n > smallSize {
		s.stats.largeReceived.Add(1)
	}
	slot := s.pool.acquire(n)
	if slot != nil {
		slot.deadline = time.Now().Add(s.opts.RequestTimeout)
	}
	if slot == nil {
		s.stats.slotDrops.Add(1)
		if n > smallSize {
			s.stats.largeDrops.Add(1)
		}
	}
	return slot
}

func (s *Server) resolve(ctx context.Context, wire, out []byte, peer netip.AddrPort, tcp bool, deadline time.Time) int {
	r := Request{Wire: wire, Peer: peer, TCP: tcp}
	err := dnswire.ParseRequest(wire, &r.Message)
	if err != nil {
		s.stats.invalid.Add(1)
		h, e := dnswire.ParseHeader(wire)
		if e != nil || h.Flags&dnswire.FlagQR != 0 {
			return 0
		}
		code := uint16(1)
		switch {
		case errors.Is(err, dnswire.ErrOpcode):
			code = 4
		case errors.Is(err, dnswire.ErrClass), errors.Is(err, dnswire.ErrUnsupported):
			code = 5
		case errors.Is(err, dnswire.ErrBadVersion):
			code = 16
		}
		if errors.Is(err, dnswire.ErrBadVersion) {
			n, _ := dnswire.BuildReply(out, &r.Message, dnswire.Reply{RCode: code}, s.opts.UDPSize)
			return n
		}
		// Only a fully decoded question is safe to reflect; malformed EDNS is omitted.
		var q dnswire.Question
		if dnswire.ParseQuestion(wire, &q) == nil {
			m := dnswire.Message{Question: q}
			n, _ := dnswire.BuildReply(out, &m, dnswire.Reply{RCode: code}, s.opts.UDPSize)
			return n
		}
		clear(out[:12])
		binary.BigEndian.PutUint16(out, h.ID)
		binary.BigEndian.PutUint16(out[2:], dnswire.FlagQR|h.Flags&(dnswire.FlagRD|dnswire.FlagCD|0x7800)|code)
		return 12
	}
	requestCtx, cancel := context.WithDeadline(ctx, deadline)
	if requestCtx.Err() != nil {
		cancel()
		n, _ := dnswire.BuildReply(out, &r.Message, dnswire.Reply{RCode: 2}, s.opts.UDPSize)
		return n
	}
	n, err := s.handler.Resolve(requestCtx, &r, out)
	expired := requestCtx.Err() != nil
	cancel()
	if err != nil || expired || n < 12 || n > len(out) {
		n, _ = dnswire.BuildReply(out, &r.Message, dnswire.Reply{RCode: 2}, s.opts.UDPSize)
		return n
	}
	budget := dnswire.UDPBudget(r.Message.EDNS, s.opts.UDPSize)
	if tcp {
		budget = 65535
	}
	n, err = dnswire.FitReply(out, out[:n], &r.Message, budget, s.opts.UDPSize)
	if err != nil {
		s.stats.invalid.Add(1)
		n, _ = dnswire.BuildReply(out, &r.Message, dnswire.Reply{RCode: 2}, s.opts.UDPSize)
	}
	return n
}
