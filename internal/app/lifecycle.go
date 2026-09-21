package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"sync"

	"github.com/richkeenan/dimsum/internal/config"
	"github.com/richkeenan/dimsum/internal/resolve"
	"github.com/richkeenan/dimsum/internal/transport"
	"github.com/richkeenan/dimsum/internal/upstream"
)

// Service is a single-start owner of sockets. StartForwarding attaches DNS;
// Start only binds sockets for custom handlers. Administration is still a listener.
type Service struct {
	mu        sync.Mutex
	started   bool
	stopping  bool
	stop      chan struct{}
	done      chan struct{}
	listeners Listeners
	addresses Addresses
	ready     bool
	err       error
}
type Addresses struct {
	DNS   []string
	Admin string
}

// Callers may serve on these sockets but Service retains close ownership.
type Listeners struct {
	TCP   []net.Listener
	UDP   []net.PacketConn
	Admin net.Listener
}

var ErrStarted = errors.New("service already started")

func (s *Service) Start(ctx context.Context, c config.Config) error {
	return s.start(ctx, c, nil)
}

// StartForwarding binds all sockets transactionally and attaches the DNS pipeline.
// Start remains available for callers that attach their own transport handlers.
func (s *Service) StartForwarding(ctx context.Context, c config.Config) error {
	if err := config.Validate(c); err != nil {
		return err
	}
	endpoints := make([]netip.AddrPort, len(c.DNS.Upstreams))
	for i, a := range c.DNS.Upstreams {
		endpoints[i] = netip.MustParseAddrPort(a)
	}
	u, err := upstream.New(upstream.Options{Endpoints: endpoints})
	if err != nil {
		return err
	}
	server, err := transport.New(transport.Options{}, resolve.New(u))
	if err != nil {
		return err
	}
	return s.start(ctx, c, server)
}

func (s *Service) start(ctx context.Context, c config.Config, server *transport.Server) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return ErrStarted
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := config.Validate(c); err != nil {
		return err
	}
	var sockets Listeners
	var addresses Addresses
	success := false
	defer func() {
		if !success {
			closeListeners(sockets)
		}
	}()
	lc := net.ListenConfig{}
	for _, a := range c.DNS.Listen {
		tcp, err := lc.Listen(ctx, "tcp", a)
		if err != nil {
			return fmt.Errorf("dns TCP %s: %w", a, err)
		}
		sockets.TCP = append(sockets.TCP, tcp)
		bound := tcp.Addr().String()
		udp, err := lc.ListenPacket(ctx, "udp", bound)
		if err != nil {
			return fmt.Errorf("dns UDP %s: %w", bound, err)
		}
		sockets.UDP = append(sockets.UDP, udp)
		addresses.DNS = append(addresses.DNS, bound)
	}
	admin, err := lc.Listen(ctx, "tcp", c.Admin.Listen)
	if err != nil {
		return fmt.Errorf("admin %s: %w", c.Admin.Listen, err)
	}
	sockets.Admin = admin
	addresses.Admin = admin.Addr().String()
	if err := ctx.Err(); err != nil {
		return err
	}
	s.listeners = sockets
	s.addresses = addresses
	s.started = true
	s.stop = make(chan struct{})
	s.done = make(chan struct{})
	runCtx, cancel := context.WithCancel(ctx)
	var workers sync.WaitGroup
	failures := make(chan error, 1)
	serve := func(fn func() error) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			err := fn()
			if runCtx.Err() == nil {
				if err == nil {
					err = errors.New("DNS transport stopped unexpectedly")
				}
				select {
				case failures <- err:
				default:
				}
			}
		}()
	}
	if server != nil {
		for _, listener := range sockets.TCP {
			serve(func() error { return server.ServeTCP(runCtx, listener) })
		}
		for _, packet := range sockets.UDP {
			serve(func() error { return server.ServeUDP(runCtx, packet.(*net.UDPConn)) })
		}
		s.ready = true
	}
	success = true
	go func() {
		var failure error
		select {
		case <-ctx.Done():
		case <-s.stop:
		case failure = <-failures:
		}
		s.mu.Lock()
		s.ready = false
		s.err = failure
		s.mu.Unlock()
		cancel()
		closeListeners(sockets)
		workers.Wait()
		close(s.done)
	}()
	return nil
}

// Ready means DNS handlers are attached; it does not assert upstream health or
// administration API availability. Err reports an unexpected DNS serving exit.
func (s *Service) Ready() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.ready }
func (s *Service) Err() error  { s.mu.Lock(); defer s.mu.Unlock(); return s.err }

func closeListeners(l Listeners) {
	for _, v := range l.TCP {
		v.Close()
	}
	for _, v := range l.UDP {
		v.Close()
	}
	if l.Admin != nil {
		l.Admin.Close()
	}
}
func (s *Service) StartFile(ctx context.Context, path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	d, err := config.Parse(b)
	if err != nil {
		return err
	}
	return s.Start(ctx, d.Config())
}
func (s *Service) Close() error {
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return nil
	}
	if !s.stopping {
		close(s.stop)
		s.stopping = true
	}
	done := s.done
	s.mu.Unlock()
	<-done
	return nil
}
func (s *Service) Done() <-chan struct{} { s.mu.Lock(); defer s.mu.Unlock(); return s.done }
func (s *Service) Addresses() Addresses {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.addresses
	a.DNS = append([]string(nil), a.DNS...)
	return a
}
func (s *Service) Listeners() Listeners {
	s.mu.Lock()
	defer s.mu.Unlock()
	l := s.listeners
	l.TCP = append([]net.Listener(nil), l.TCP...)
	l.UDP = append([]net.PacketConn(nil), l.UDP...)
	return l
}
