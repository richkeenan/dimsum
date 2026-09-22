// Package upstream owns bounded exchanges. Each attempt exclusively leases its
// socket and owns query/response storage; no borrowed bytes survive Exchange.
package upstream

import (
	"context"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"sync"
	"time"

	"github.com/richkeenan/dimsum/internal/dnswire"
)

var ErrOverloaded = errors.New("upstream: outstanding or ID capacity exhausted")

type Options struct {
	Endpoints    []Endpoint
	Fallback     []Endpoint
	BootstrapDNS []netip.AddrPort
	// RootCAs optionally supplies an application's trust store. Nil uses system trust.
	RootCAs                       *x509.CertPool
	Mode                          string
	MaxAttempts, FailureThreshold int
	OpenInterval, MaxBackoff      time.Duration
	MaxOutstanding                int
	Timeout, AttemptTimeout       time.Duration
}
type Client struct {
	options     Options
	slots       chan struct{}
	ids         ids
	mu          sync.Mutex
	health      []endpointHealth
	selections  uint64
	connections connections
	lifetime    context.Context
	cancel      context.CancelFunc
	bootstrap   bootstrapCache
	httpMu      sync.Mutex
	httpClients map[Endpoint]*http.Client
}

// ExchangeResult refers only to validated bytes copied to caller-owned output.
// The caller must use dnswire.PersonalizeReply (or equivalent semantic checks),
// not blindly patch client ID/case/flags: compressed names can depend on them.
type ExchangeResult struct {
	N, Attempts int
	Endpoint    Endpoint
	Transport   string
	TCP         bool
	Route       RouteKey
	EndpointID  uint32
	Fallback    bool
	Probes      int
	Background  bool // set by resolver observation, never inferred from client traffic
}

// ValidateOptions checks defaults and limits without allocating transport state.
func ValidateOptions(o Options) error { return normalizeOptions(&o) }

func normalizeOptions(o *Options) error {
	if len(o.Endpoints) == 0 || len(o.Endpoints) > 16 {
		return errors.New("upstream: require 1..16 endpoints")
	}
	if len(o.Fallback) > 16 {
		return errors.New("upstream: at most 16 fallback endpoints")
	}
	for _, endpoints := range [][]Endpoint{o.Endpoints, o.Fallback} {
		for _, a := range endpoints {
			if _, err := ParseEndpoint(a.String()); err != nil {
				return err
			}
		}
	}
	if o.BootstrapDNS == nil {
		o.BootstrapDNS = []netip.AddrPort{netip.MustParseAddrPort("1.1.1.1:53"), netip.MustParseAddrPort("9.9.9.9:53")}
	}
	if len(o.BootstrapDNS) < 1 || len(o.BootstrapDNS) > 16 {
		return errors.New("upstream: require 1..16 bootstrap endpoints")
	}
	for _, a := range o.BootstrapDNS {
		if a.Port() == 0 || !unicast(a.Addr()) {
			return errors.New("upstream: invalid bootstrap endpoint")
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
	if o.Mode == "" {
		o.Mode = "ordered"
	}
	if o.MaxAttempts == 0 {
		o.MaxAttempts = 3
	}
	if o.FailureThreshold == 0 {
		o.FailureThreshold = 2
	}
	if o.OpenInterval == 0 {
		o.OpenInterval = 5 * time.Second
	}
	if o.MaxBackoff == 0 {
		o.MaxBackoff = 60 * time.Second
	}
	if (o.Mode != "ordered" && o.Mode != "adaptive") || o.MaxAttempts < 1 || o.MaxAttempts > 16 || o.FailureThreshold < 1 || o.FailureThreshold > 100 || o.OpenInterval < 0 || o.OpenInterval > time.Minute || o.MaxBackoff < o.OpenInterval || o.MaxBackoff > time.Minute {
		return errors.New("upstream: invalid pool settings")
	}
	if o.MaxOutstanding < 1 || o.MaxOutstanding > 65536 || o.Timeout < 0 || o.Timeout > time.Minute || o.AttemptTimeout < 0 || o.AttemptTimeout > time.Minute {
		return errors.New("upstream: invalid limits")
	}
	return nil
}

func New(o Options) (*Client, error) {
	if err := normalizeOptions(&o); err != nil {
		return nil, err
	}
	o.Endpoints = append([]Endpoint(nil), o.Endpoints...)
	o.Fallback = append([]Endpoint(nil), o.Fallback...)
	o.BootstrapDNS = append([]netip.AddrPort(nil), o.BootstrapDNS...)
	if o.RootCAs != nil {
		o.RootCAs = o.RootCAs.Clone()
	}
	lifetime, cancel := context.WithCancel(context.Background())
	return &Client{options: o, slots: make(chan struct{}, o.MaxOutstanding), health: make([]endpointHealth, len(o.Endpoints)+len(o.Fallback)), lifetime: lifetime, cancel: cancel, httpClients: make(map[Endpoint]*http.Client)}, nil
}
func (c *Client) Outstanding() int { return len(c.slots) }

func (c *Client) Exchange(parent context.Context, wire, out []byte) (result ExchangeResult, returned error) {
	if c.connections.isClosed() {
		return result, net.ErrClosed
	}
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
	stop := context.AfterFunc(c.lifetime, cancel)
	defer stop()
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
	probes := 0
	defer func() { result.Attempts = attempts; result.Probes = probes }()
	for _, index := range c.order() {
		if attempts >= c.options.MaxAttempts || ctx.Err() != nil {
			break
		}
		eligible, epoch, probe := c.claimDetailed(index)
		if !eligible {
			continue
		}
		if probe {
			probes++
		}
		endpoint := c.endpoint(index)
		started := time.Now()
		attempts++
		tcp := len(query) > 1232
		n, m, e := c.attempt(ctx, endpoint, query, &q, buf, tcp, false)
		if e == nil && endpoint.Transport() == "udp" && !tcp && m.Question.Header.Flags&dnswire.FlagTC != 0 {
			if attempts >= c.options.MaxAttempts {
				last = ErrResponse
				c.record(index, epoch, time.Since(started), nil, 0, true)
				break
			}
			attempts++
			tcp = true
			n, m, e = c.attempt(ctx, endpoint, query, &q, buf, true, false)
		}
		// A retired idle peer is recoverable, but the fresh lease is a real
		// attempt under the same overall deadline and shared retry budget.
		var stale *reusedTransportError
		if errors.As(e, &stale) && attempts < c.options.MaxAttempts && ctx.Err() == nil {
			attempts++
			n, m, e = c.attempt(ctx, endpoint, query, &q, buf, tcp, true)
		}
		if e == nil && m.Question.Header.Flags&dnswire.FlagTC != 0 {
			e = ErrResponse
		}
		if errors.Is(e, ErrOverloaded) {
			// Local admission failed before sending this attempt. The shared ID
			// table cannot be relieved by trying another endpoint. Release any
			// half-open lease without recording an endpoint outcome or backoff.
			attempts--
			c.record(index, epoch, time.Since(started), nil, 0, true)
			return result, e
		}
		c.record(index, epoch, time.Since(started), e, m.RCode, parent.Err() != nil || c.lifetime.Err() != nil)
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
		transport := endpoint.Transport()
		if transport == "udp" && tcp {
			transport = "tcp"
		}
		return ExchangeResult{N: n, Attempts: attempts, Endpoint: endpoint, Transport: transport, TCP: transport == "tcp", Route: DefaultRoute, EndpointID: uint32(index + 1), Fallback: index >= len(c.options.Endpoints)}, nil
	}
	if ctx.Err() != nil {
		last = ctx.Err()
	}
	return result, last
}

func (c *Client) attempt(parent context.Context, endpoint Endpoint, query []byte, q *dnswire.Question, out []byte, tcp, fresh bool) (int, dnswire.Message, error) {
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
	if endpoint.Transport() == "doh" {
		n, m, err = c.exchangeDoH(ctx, endpoint, query, q, id, out)
	} else if tcp || endpoint.Transport() == "dot" {
		n, m, err = c.exchangeTCP(ctx, endpoint, query, q, id, out, fresh)
	} else {
		n, m, err = exchangeUDP(ctx, netip.AddrPortFrom(endpoint.Addr(), endpoint.Port()), query, q, id, out)
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
