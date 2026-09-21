package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"

	"github.com/richkeenan/dimsum/internal/config"
)

// Service is a single-start owner of sockets. DNS and administration handlers
// attach in subsequent tasks; opening sockets does not yet imply DNS readiness.
type Service struct {
	mu        sync.Mutex
	started   bool
	stopping  bool
	stop      chan struct{}
	done      chan struct{}
	listeners Listeners
	addresses Addresses
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
	success = true
	go func() {
		select {
		case <-ctx.Done():
		case <-s.stop:
		}
		closeListeners(sockets)
		close(s.done)
	}()
	return nil
}

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
