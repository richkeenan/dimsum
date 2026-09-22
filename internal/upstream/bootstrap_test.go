package upstream

import (
	"context"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func bootstrapFixture(t *testing.T, ttl uint32, mode string) (netip.AddrPort, *atomic.Int32) {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	calls := new(atomic.Int32)
	server := &dns.Server{PacketConn: conn, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, q *dns.Msg) {
		calls.Add(1)
		if mode == "slow" {
			time.Sleep(40 * time.Millisecond)
		}
		if mode == "drop" {
			return
		}
		r := new(dns.Msg)
		r.SetReply(q)
		name := q.Question[0].Name
		if mode == "unrelated" {
			name = "rogue.example."
		}
		h := dns.RR_Header{Name: name, Rrtype: q.Question[0].Qtype, Class: dns.ClassINET, Ttl: ttl}
		if h.Rrtype == dns.TypeA {
			r.Answer = []dns.RR{&dns.A{Hdr: h, A: net.ParseIP("192.0.2.10")}}
		} else {
			r.Answer = []dns.RR{&dns.AAAA{Hdr: h, AAAA: net.ParseIP("2001:db8::10")}}
		}
		if mode == "local" {
			if h.Rrtype == dns.TypeA {
				r.Answer = []dns.RR{&dns.A{Hdr: h, A: net.ParseIP("127.0.0.1")}}
			} else {
				r.Answer = []dns.RR{&dns.AAAA{Hdr: h, AAAA: net.ParseIP("::1")}}
			}
		}
		if mode == "cname-zero" {
			r.Answer[0].Header().Name = "target.example."
			r.Answer = append([]dns.RR{&dns.CNAME{Hdr: dns.RR_Header{Name: name, Rrtype: dns.TypeCNAME, Class: dns.ClassINET, Ttl: 0}, Target: "target.example."}}, r.Answer...)
		}
		if mode == "id" {
			r.Id++
		}
		_ = w.WriteMsg(r)
	})}
	started := make(chan struct{})
	server.NotifyStartedFunc = func() { close(started) }
	go func() { _ = server.ActivateAndServe() }()
	<-started
	t.Cleanup(func() { _ = server.Shutdown() })
	return netip.MustParseAddrPort(conn.LocalAddr().String()), calls
}

func TestBootstrapZeroAndAliasTTLDoNotCache(t *testing.T) {
	for _, mode := range []string{"zero", "cname-zero"} {
		t.Run(mode, func(t *testing.T) {
			ttl := uint32(60)
			if mode == "zero" {
				ttl = 0
			}
			a, calls := bootstrapFixture(t, ttl, mode)
			e, err := ParseEndpoint("tls://dns.example")
			require.NoError(t, err)
			c, err := New(Options{Endpoints: []Endpoint{e}, BootstrapDNS: []netip.AddrPort{a}})
			require.NoError(t, err)
			defer c.Close()
			for range 2 {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				addresses, err := c.bootstrapAddresses(ctx, e)
				cancel()
				require.NoError(t, err)
				assert.Len(t, addresses, 2)
			}
			assert.EqualValues(t, 4, calls.Load())
		})
	}
}
func TestBootstrapCacheFamiliesExpiryAndOwnership(t *testing.T) {
	a, calls := bootstrapFixture(t, 1, "")
	e, err := ParseEndpoint("tls://dns.example")
	require.NoError(t, err)
	c, err := New(Options{Endpoints: []Endpoint{e}, BootstrapDNS: []netip.AddrPort{a}})
	require.NoError(t, err)
	defer c.Close()
	lookup := func() []netip.Addr {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		got, e := c.bootstrapAddresses(ctx, e)
		require.NoError(t, e)
		return got
	}
	got := lookup()
	assert.ElementsMatch(t, []netip.Addr{netip.MustParseAddr("192.0.2.10"), netip.MustParseAddr("2001:db8::10")}, got)
	got[0] = netip.Addr{}
	assert.NotContains(t, lookup(), netip.Addr{})
	assert.EqualValues(t, 2, calls.Load())
	time.Sleep(1100 * time.Millisecond)
	lookup()
	assert.EqualValues(t, 4, calls.Load())
}
func TestBootstrapInvalidAndCancelled(t *testing.T) {
	for _, mode := range []string{"unrelated", "id", "drop"} {
		t.Run(mode, func(t *testing.T) {
			a, _ := bootstrapFixture(t, 60, mode)
			e, err := ParseEndpoint("tls://dns.example")
			require.NoError(t, err)
			c, err := New(Options{Endpoints: []Endpoint{e}, BootstrapDNS: []netip.AddrPort{a}})
			require.NoError(t, err)
			defer c.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
			defer cancel()
			got, err := c.bootstrapAddresses(ctx, e)
			assert.Error(t, err)
			assert.Empty(t, got)
		})
	}
}
func TestBootstrapConcurrentCache(t *testing.T) {
	a, calls := bootstrapFixture(t, 60, "")
	e, err := ParseEndpoint("tls://dns.example")
	require.NoError(t, err)
	c, err := New(Options{Endpoints: []Endpoint{e}, BootstrapDNS: []netip.AddrPort{a}})
	require.NoError(t, err)
	defer c.Close()
	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_, err := c.bootstrapAddresses(ctx, e)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		assert.NoError(t, err)
	}
	assert.EqualValues(t, 2, calls.Load())
}

func TestLookupIPUsesConfiguredPool(t *testing.T) {
	a, calls := bootstrapFixture(t, 60, "slow")
	c, err := New(Options{Endpoints: []Endpoint{PlainEndpoint(a)}, MaxOutstanding: 1})
	require.NoError(t, err)
	defer c.Close()
	addresses, err := c.LookupIP(context.Background(), "subscription.example")
	require.NoError(t, err)
	assert.ElementsMatch(t, []netip.Addr{netip.MustParseAddr("192.0.2.10"), netip.MustParseAddr("2001:db8::10")}, addresses)
	assert.EqualValues(t, 2, calls.Load())
}

func TestBootstrapWaiterCancellationDoesNotCancelLeader(t *testing.T) {
	a, calls := bootstrapFixture(t, 60, "slow")
	e, err := ParseEndpoint("tls://dns.example")
	require.NoError(t, err)
	c, err := New(Options{Endpoints: []Endpoint{e}, BootstrapDNS: []netip.AddrPort{a}})
	require.NoError(t, err)
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := c.bootstrapAddresses(ctx, e); done <- err }()
	require.Eventually(t, func() bool { return calls.Load() > 0 }, time.Second, time.Millisecond)
	waiter, stop := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer stop()
	_, err = c.bootstrapAddresses(waiter, e)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	require.NoError(t, <-done)
	addresses, err := c.bootstrapAddresses(ctx, e)
	require.NoError(t, err)
	assert.Len(t, addresses, 2)
	assert.EqualValues(t, 2, calls.Load())
}

func TestBootstrapAnswerBoundsAndOwners(t *testing.T) {
	q := new(dns.Msg)
	q.SetQuestion("resolver.example.", dns.TypeA)
	response := new(dns.Msg)
	response.SetReply(q)
	response.Answer = append(response.Answer, &dns.A{Hdr: dns.RR_Header{Name: "unrelated.example.", Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60}, A: net.ParseIP("192.0.2.250")})
	for i := 1; i <= 40; i++ {
		response.Answer = append(response.Answer, &dns.A{Hdr: dns.RR_Header{Name: "resolver.example.", Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60}, A: net.IPv4(192, 0, 2, byte(i))})
	}
	wire, err := response.Pack()
	require.NoError(t, err)
	addresses, _, err := bootstrapAnswer(wire, dns.TypeA)
	require.NoError(t, err)
	assert.Len(t, addresses, 16)
	assert.NotContains(t, addresses, netip.MustParseAddr("192.0.2.250"))
}
