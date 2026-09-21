// Package upstream owns bounded exchanges. Each attempt owns its socket, query,
// and response storage; no borrowed bytes survive Exchange. There is no cache.
package upstream

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"net/netip"
	"time"

	"github.com/richkeenan/dimsum/internal/dnswire"
)

var ErrOverloaded = errors.New("upstream: outstanding or ID capacity exhausted")

type Options struct {
	Endpoints               []netip.AddrPort
	MaxOutstanding          int
	Timeout, AttemptTimeout time.Duration
}
type Client struct {
	options Options
	slots   chan struct{}
	ids     ids
}

// ExchangeResult refers only to validated bytes copied to caller-owned output.
// The caller must use dnswire.PersonalizeReply (or equivalent semantic checks),
// not blindly patch client ID/case/flags: compressed names can depend on them.
type ExchangeResult struct {
	N, Attempts int
	Endpoint    netip.AddrPort
	TCP         bool
}

func New(o Options) (*Client, error) {
	if len(o.Endpoints) == 0 || len(o.Endpoints) > 16 {
		return nil, errors.New("upstream: require 1..16 endpoints")
	}
	for _, a := range o.Endpoints {
		if !a.IsValid() || a.Port() == 0 || a.Addr().IsUnspecified() || a.Addr().IsMulticast() {
			return nil, errors.New("upstream: require unicast literal IP and nonzero port")
		}
	}
	if o.MaxOutstanding == 0 {
		o.MaxOutstanding = 1024
	}
	if o.Timeout == 0 {
		o.Timeout = 2 * time.Second
	}
	if o.AttemptTimeout == 0 {
		o.AttemptTimeout = 750 * time.Millisecond
	}
	if o.MaxOutstanding < 1 || o.MaxOutstanding > 65536 || o.Timeout < 0 || o.Timeout > time.Minute || o.AttemptTimeout < 0 || o.AttemptTimeout > time.Minute {
		return nil, errors.New("upstream: invalid limits")
	}
	o.Endpoints = append([]netip.AddrPort(nil), o.Endpoints...)
	return &Client{options: o, slots: make(chan struct{}, o.MaxOutstanding)}, nil
}
func (c *Client) Outstanding() int { return len(c.slots) }

func (c *Client) Exchange(parent context.Context, wire, out []byte) (ExchangeResult, error) {
	var result ExchangeResult
	if err := parent.Err(); err != nil {
		return result, err
	}
	select {
	case c.slots <- struct{}{}:
	default:
		return result, ErrOverloaded
	}
	defer func() { <-c.slots }()
	ctx, cancel := context.WithTimeout(parent, c.options.Timeout)
	defer cancel()
	query, q, err := prepare(wire)
	if err != nil {
		return result, err
	}
	// RD=0 must not recurse, even for direct users of this package.
	if q.Header.Flags&dnswire.FlagRD == 0 {
		return result, dnswire.ErrUnsupported
	}
	buf := make([]byte, 65535)
	last := error(ErrResponse)
	attempts := 0
	for _, endpoint := range c.options.Endpoints {
		if attempts >= 3 || ctx.Err() != nil {
			break
		}
		attempts++
		tcp := len(query) > 1232
		n, m, e := c.attempt(ctx, endpoint, query, &q, buf, tcp)
		if e == nil && !tcp && m.Question.Header.Flags&dnswire.FlagTC != 0 {
			if attempts >= 3 {
				last = ErrResponse
				break
			}
			attempts++
			tcp = true
			n, m, e = c.attempt(ctx, endpoint, query, &q, buf, true)
		}
		if e == nil && m.Question.Header.Flags&dnswire.FlagTC != 0 {
			e = ErrResponse
		}
		if e != nil {
			last = e
			continue
		}
		if m.RCode == 2 || m.RCode == 5 {
			last = ErrResponse
			continue
		}
		if err = ctx.Err(); err != nil {
			return result, err
		}
		deadline, _ := ctx.Deadline()
		if !time.Now().Before(deadline) {
			return result, context.DeadlineExceeded
		}
		if len(out) < n {
			return result, dnswire.ErrBounds
		}
		copy(out, buf[:n])
		return ExchangeResult{N: n, Attempts: attempts, Endpoint: endpoint, TCP: tcp}, nil
	}
	if ctx.Err() != nil {
		last = ctx.Err()
	}
	return result, last
}

func (c *Client) attempt(parent context.Context, endpoint netip.AddrPort, query []byte, q *dnswire.Question, out []byte, tcp bool) (int, dnswire.Message, error) {
	ctx, cancel := context.WithTimeout(parent, c.options.AttemptTimeout)
	defer cancel()
	id, err := c.ids.acquire()
	if err != nil {
		return 0, dnswire.Message{}, err
	}
	quarantine := max(4*time.Second, 2*c.options.Timeout)
	defer c.ids.release(id, quarantine)
	binary.BigEndian.PutUint16(query, id)
	var n int
	var m dnswire.Message
	if tcp {
		n, m, err = exchangeTCP(ctx, endpoint, query, q, id, out)
	} else {
		n, m, err = exchangeUDP(ctx, endpoint, query, q, id, out)
	}
	if ctx.Err() != nil {
		err = ctx.Err()
	} else if deadline, _ := ctx.Deadline(); !time.Now().Before(deadline) {
		err = context.DeadlineExceeded
	}
	return n, m, err
}

func exchangeUDP(ctx context.Context, endpoint netip.AddrPort, query []byte, q *dnswire.Question, id uint16, out []byte) (int, dnswire.Message, error) {
	var conn net.Conn
	var err error
	// The OS ephemeral-port allocator is not assumed random. Choose a fresh
	// cryptographic port from the full unprivileged range and retry bind collisions.
	for tries := 0; tries < 32; tries++ {
		port, e := random16()
		if e != nil {
			return 0, dnswire.Message{}, e
		}
		if port < 1024 {
			continue
		}
		d := net.Dialer{LocalAddr: &net.UDPAddr{Port: int(port)}}
		conn, err = d.DialContext(ctx, "udp", endpoint.String())
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			break
		}
	}
	if conn == nil {
		if err == nil {
			err = ErrOverloaded
		}
		return 0, dnswire.Message{}, err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	deadline, _ := ctx.Deadline()
	if err = conn.SetDeadline(deadline); err != nil {
		return 0, dnswire.Message{}, err
	}
	if _, err = conn.Write(query); err != nil {
		return 0, dnswire.Message{}, err
	}
	for {
		n, err := conn.Read(out)
		if err != nil {
			return 0, dnswire.Message{}, err
		}
		if ctx.Err() != nil || !time.Now().Before(deadline) {
			return 0, dnswire.Message{}, context.DeadlineExceeded
		}
		m, err := validate(out[:n], q, id)
		if err == nil {
			return n, m, nil
		}
	}
}
