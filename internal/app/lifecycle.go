package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"sync"
	"time"

	"github.com/richkeenan/dimsum/internal/clients"
	"github.com/richkeenan/dimsum/internal/config"
	"github.com/richkeenan/dimsum/internal/policy"
	"github.com/richkeenan/dimsum/internal/resolve"
	"github.com/richkeenan/dimsum/internal/transport"
	"github.com/richkeenan/dimsum/internal/upstream"
)

// Service is a single-start owner of sockets. StartForwarding attaches DNS;
// Start only binds sockets for custom handlers. Administration is still a listener.
type Service struct {
	mu            sync.Mutex
	started       bool
	stopping      bool
	stop          chan struct{}
	done          chan struct{}
	listeners     Listeners
	addresses     Addresses
	ready         bool
	err           error
	names         *clients.Manager
	pipeline      *resolve.Pipeline
	transport     *transport.Server
	observability *observability
	dhcp          *DHCPSupervisor
}
type Addresses struct {
	DNS     []string
	Admin   string
	Control string
}

// Callers may serve on these sockets but Service retains close ownership.
type Listeners struct {
	TCP     []net.Listener
	UDP     []net.PacketConn
	Admin   net.Listener
	Control net.Listener
}

var ErrStarted = errors.New("service already started")

func (s *Service) Start(ctx context.Context, c config.Config) error {
	return s.start(ctx, c, nil, nil, nil, nil, nil)
}

// StartForwarding binds all sockets transactionally and attaches the DNS pipeline.
// Start remains available for callers that attach their own transport handlers.
func (s *Service) StartForwarding(ctx context.Context, c config.Config) error {
	return s.startForwarding(ctx, c, nil)
}

// StartManaged owns the automatic watcher for the lifetime of the listeners.
func (s *Service) StartManaged(ctx context.Context, store *config.Store) error {
	if store == nil || store.Snapshot() == nil {
		return fmt.Errorf("service: active configuration required")
	}
	return s.startForwarding(ctx, store.Snapshot().Config(), store)
}

func (s *Service) startForwarding(ctx context.Context, c config.Config, store *config.Store) error {
	if err := config.Validate(c); err != nil {
		return err
	}
	if store == nil && (len(c.Lists) > 0 || len(c.Rules) > 0 || len(c.Records) > 0 || len(c.Zones) > 0 || len(c.Clients) > 0 || c.Filtering != (policy.Settings{}) || !c.Naming.IsZero()) {
		return fmt.Errorf("service: policy configuration requires StartManaged")
	}
	if err := upstream.ValidateOptions(c.DNS.UpstreamOptions()); err != nil {
		return err
	}
	var u *upstream.Client
	if store == nil {
		var err error
		u, err = upstream.New(c.DNS.UpstreamOptions())
		if err != nil {
			return err
		}
	}
	var names *clients.Manager
	leaseNames := newDHCPNames(s.DHCPView)
	if store != nil {
		names = clients.New(func() *clients.View { return store.Snapshot().Names() })
		names.SetDHCP(func(view *clients.View, address netip.Addr) (clients.Name, bool) {
			return leaseNames.name(store.Snapshot(), view, address)
		})
	}
	pipeline := resolve.NewWithNames(u, store, names)
	pipeline.SetLeases(leaseNames.capture)
	var observations *observability
	options := transport.Options{}
	if store != nil {
		var err error
		observations, err = newObservability(store)
		if err != nil {
			pipeline.Close()
			return err
		}
		observations.retention(c.Statistics)
		observations.start()
		options.Observe = observations.observe
		pipeline.SetExchangeObserver(observations.exchange)
	}
	server, err := transport.New(options, pipeline)
	if err != nil {
		pipeline.Close()
		if observations != nil {
			observations.close()
		}
		return err
	}
	err = s.start(ctx, c, server, store, names, func() {
		pipeline.Close()
		if observations != nil {
			observations.close()
		}
	}, observations)
	if err != nil {
		pipeline.Close()
		if observations != nil {
			observations.close()
		}
	} else {
		s.mu.Lock()
		s.pipeline = pipeline
		s.transport = server
		s.observability = observations
		s.mu.Unlock()
	}
	return err
}

func (s *Service) start(ctx context.Context, c config.Config, server *transport.Server, store *config.Store, names *clients.Manager, cleanup func(), observations *observability) error {
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
	var managed *managedRuntime
	success := false
	defer func() {
		if !success {
			closeListeners(sockets)
			if managed != nil {
				managed.control.Close()
			}
		}
	}()
	lc := net.ListenConfig{}
	for _, a := range c.DNS.Listen {
		// Preserve explicit IPv4 capability in both the socket and its reported
		// address. Generic tcp may report [::] for an IPv4 wildcard; treating
		// arbitrary IPv6 listeners as IPv4-capable would be incorrect.
		tcpNetwork, udpNetwork := "tcp", "udp"
		if address, err := netip.ParseAddrPort(a); err == nil && address.Addr().Is4() {
			tcpNetwork, udpNetwork = "tcp4", "udp4"
		}
		tcp, err := lc.Listen(ctx, tcpNetwork, a)
		if err != nil {
			return fmt.Errorf("dns TCP %s: %w", a, err)
		}
		sockets.TCP = append(sockets.TCP, tcp)
		bound := tcp.Addr().String()
		udp, err := lc.ListenPacket(ctx, udpNetwork, bound)
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
	bound := c
	bound.DNS.Listen = addresses.DNS
	if err := config.Validate(bound); err != nil {
		return err
	}
	if store != nil {
		if err := store.BindDNSListeners(addresses.DNS); err != nil {
			return err
		}
		managed, err = newManagedRuntime(s, store, observations, addresses.Admin)
		if err != nil {
			return err
		}
		sockets.Control = managed.local
		addresses.Control = managed.socket
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.listeners = sockets
	s.names = names
	s.addresses = addresses
	s.started = true
	s.stop = make(chan struct{})
	s.done = make(chan struct{})
	runCtx, cancel := context.WithCancel(ctx)
	dataDir := c.Paths.DataDir
	if store != nil {
		dataDir = store.ResolvePath(dataDir)
	}
	dhcpDNS := dhcpIPv4Listeners(sockets.TCP, sockets.UDP)
	if server == nil {
		dhcpDNS = nil
	} // bound custom-handler sockets are not DNS readiness
	s.dhcp = NewDHCPSupervisor(dataDir, dhcpDNS)
	if managed != nil {
		managed.dhcp = s.dhcp
	}
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
	if store != nil {
		workers.Add(1)
		go func() { defer workers.Done(); store.Watch(runCtx) }()
	}
	if names != nil {
		workers.Add(1)
		go func() { defer workers.Done(); names.Run(runCtx) }()
		if observations != nil && observations.db != nil {
			workers.Add(1)
			go func() { defer workers.Done(); runDNSGuesses(runCtx, observations.db, names) }()
		}
	}
	if managed != nil {
		serve(func() error { return serveHTTP(runCtx, sockets.Admin, managed.handler) })
		serve(func() error { return serveHTTP(runCtx, sockets.Control, managed.admin.LocalHandler()) })
		workers.Add(1)
		go func() { defer workers.Done(); managed.watch(runCtx) }()
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
	if store == nil && c.DHCP.Enabled {
		workers.Add(1)
		go func() { defer workers.Done(); _ = s.dhcp.Reconcile(runCtx, c.DHCP, 1) }()
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
		dhcpClose, dhcpCancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = s.dhcp.Close(dhcpClose)
		dhcpCancel()
		closeListeners(sockets)
		if managed != nil {
			managed.control.Close()
		}
		workers.Wait()
		if cleanup != nil {
			cleanup()
		}
		close(s.done)
	}()
	return nil
}

// Ready means DNS handlers are attached; it does not assert upstream health or
// administration API availability. Err reports an unexpected DNS serving exit.
func (s *Service) Ready() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.ready }
func (s *Service) ClientName(address netip.Addr) clients.Name {
	s.mu.Lock()
	m := s.names
	s.mu.Unlock()
	if m == nil {
		return clients.Name{Address: address, Source: "unknown"}
	}
	return m.Get(address)
}
func (s *Service) Err() error { s.mu.Lock(); defer s.mu.Unlock(); return s.err }
func (s *Service) NamingDiagnostics() clients.DiscoveryDiagnostics {
	s.mu.Lock()
	m := s.names
	s.mu.Unlock()
	if m == nil {
		return clients.DiscoveryDiagnostics{}
	}
	return m.Diagnostics()
}

func (s *Service) DNSStats() (transport.Stats, resolve.CacheStats) {
	s.mu.Lock()
	p, server := s.pipeline, s.transport
	s.mu.Unlock()
	var transportStats transport.Stats
	var cacheStats resolve.CacheStats
	if server != nil {
		transportStats = server.Stats()
	}
	if p != nil {
		cacheStats = p.CacheStats()
	}
	return transportStats, cacheStats
}

func (s *Service) UpstreamHealth() []upstream.Health {
	s.mu.Lock()
	p := s.pipeline
	s.mu.Unlock()
	if p == nil {
		return nil
	}
	return p.UpstreamHealth()
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
	if l.Control != nil {
		l.Control.Close()
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
