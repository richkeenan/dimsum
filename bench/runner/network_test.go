package runner_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/richkeenan/dimsum/bench/runner"
	"github.com/richkeenan/dimsum/bench/workload"
	"github.com/richkeenan/dimsum/internal/config"
	"github.com/richkeenan/dimsum/internal/resolve"
	"github.com/richkeenan/dimsum/internal/testutil"
	"github.com/richkeenan/dimsum/internal/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The oracle and client are deliberately test-only. Each exchange uses an actual
// loopback socket; client packing, dialing and validation are included in timing.
type networkFixture struct {
	address string
	p       *resolve.Pipeline
	clock   *testutil.Clock
	calls   atomic.Int64
	delay   atomic.Bool
	drop    atomic.Bool
	fail    atomic.Bool
}

func newNetworkFixture(t testing.TB) *networkFixture {
	t.Helper()
	f := &networkFixture{clock: testutil.NewClock(time.Now())}
	errs := make(chan error, 64)
	u, err := testutil.NewUpstream(f.clock, func(r testutil.Request) testutil.Response {
		f.calls.Add(1)
		var q dns.Msg
		if err := q.Unpack(r.Wire); err != nil {
			errs <- err
			return testutil.Response{Drop: true}
		}
		m := new(dns.Msg)
		m.SetReply(&q)
		m.RecursionAvailable = true
		if q.IsEdns0() != nil {
			m.SetEdns0(1232, false)
		}
		name := q.Question[0].Name
		ttl := uint32(60)
		if name == "stale.bench.test." {
			ttl = 1
		}
		switch name {
		case "negative.bench.test.":
			m.Rcode = dns.RcodeNameError
			m.Ns = []dns.RR{&dns.SOA{Hdr: dns.RR_Header{Name: "bench.test.", Rrtype: dns.TypeSOA, Class: dns.ClassINET, Ttl: 60}, Ns: "ns.bench.test.", Mbox: "hostmaster.bench.test.", Minttl: 60}}
		case "large.bench.test.":
			m.Answer = []dns.RR{&dns.TXT{Hdr: dns.RR_Header{Name: name, Rrtype: dns.TypeTXT, Class: dns.ClassINET, Ttl: 60}, Txt: []string{strings.Repeat("x", 200), strings.Repeat("y", 200), strings.Repeat("z", 200), strings.Repeat("w", 200), strings.Repeat("v", 200), strings.Repeat("u", 200)}}}
		default:
			m.Answer = []dns.RR{&dns.A{Hdr: dns.RR_Header{Name: name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: ttl}, A: net.IPv4(192, 0, 2, 42)}}
		}
		if f.fail.Load() {
			m.Rcode = dns.RcodeServerFailure
			m.Answer, m.Ns = nil, nil
		}
		wire, err := m.Pack()
		if err != nil {
			errs <- err
		}
		response := testutil.Response{Wire: wire, Drop: f.drop.Load()}
		if f.delay.Load() {
			response.Delay = time.Second
		}
		return response
	})
	require.NoError(t, err)
	done := make(chan struct{})
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for {
			select {
			case <-u.Requests():
			case <-done:
				return
			}
		}
	}()
	t.Cleanup(func() {
		assert.NoError(t, u.Close())
		close(done)
		<-drained
		close(errs)
		for err := range errs {
			assert.NoError(t, err)
		}
	})
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	text := fmt.Sprintf("version: 1\ndns:\n  listen: [127.0.0.1:0]\n  upstreams: [%s]\nadmin: {listen: '127.0.0.1:0'}\npaths: {data_dir: data, secrets_dir: secrets}\ncache:\n  stale_mode: immediate\nrules:\n  - {id: bench-block, kind: exact, action: deny, pattern: blocked.bench.test, enabled: true}\nfiltering:\n  mode: nxdomain\n", u.Address())
	require.NoError(t, os.WriteFile(path, []byte(text), 0600))
	store, err := config.OpenStore(context.Background(), path, filepath.Join(dir, "state"), config.StoreOptions{Offline: true})
	require.NoError(t, err)
	f.p = resolve.NewWithStore(nil, store)
	t.Cleanup(func() { assert.NoError(t, f.p.Close()) })
	tcp, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { tcp.Close() })
	udp, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: tcp.Addr().(*net.TCPAddr).Port})
	require.NoError(t, err)
	t.Cleanup(func() { udp.Close() })
	s, err := transport.New(transport.Options{SmallSlots: 32, LargeSlots: 4, Workers: 8, MaxConnections: 16}, f.p)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan error, 2)
	go func() { stopped <- s.ServeUDP(ctx, udp) }()
	go func() { stopped <- s.ServeTCP(ctx, tcp) }()
	t.Cleanup(func() {
		cancel()
		for range 2 {
			select {
			case err := <-stopped:
				assert.NoError(t, err)
			case <-time.After(3 * time.Second):
				t.Error("transport shutdown timed out")
			}
		}
	})
	f.address = tcp.Addr().String()
	return f
}

func (f *networkFixture) exchange(ctx context.Context, network, name string, id uint16, rd bool, edns uint16) (*dns.Msg, error) {
	q := new(dns.Msg)
	q.SetQuestion(name, dns.TypeA)
	if name == "large.bench.test." {
		q.Question[0].Qtype = dns.TypeTXT
	}
	q.Id, q.RecursionDesired = id, rd
	if edns != 0 {
		q.SetEdns0(edns, false)
	}
	c := dns.Client{Net: network, Timeout: 2 * time.Second, UDPSize: 4096}
	m, _, err := c.ExchangeContext(ctx, q, f.address)
	if err != nil {
		return nil, err
	}
	if m.Id != id || !m.Response || !reflect.DeepEqual(m.Question, q.Question) {
		return nil, fmt.Errorf("reply identity mismatch for %s", name)
	}
	return m, nil
}

func validateMixed(m *dns.Msg, name string) error {
	if name == "blocked.bench.test." || name == "negative.bench.test." {
		if m.Rcode != dns.RcodeNameError || len(m.Answer) != 0 {
			return fmt.Errorf("expected NXDOMAIN for %s: %s", name, m)
		}
		if name == "negative.bench.test." && len(m.Ns) != 1 {
			return fmt.Errorf("missing negative SOA")
		}
		return nil
	}
	if m.Rcode != dns.RcodeSuccess || m.Truncated || len(m.Answer) != 1 {
		return fmt.Errorf("invalid positive answer: %s", m)
	}
	a, ok := m.Answer[0].(*dns.A)
	if !ok || a.A.String() != "192.0.2.42" {
		return fmt.Errorf("incorrect address")
	}
	if name == "stale.bench.test." && a.Hdr.Ttl != 30 {
		return fmt.Errorf("expected stale TTL, got %d", a.Hdr.Ttl)
	}
	return nil
}

var mixedNames = []string{"warm.bench.test.", "blocked.bench.test.", "negative.bench.test.", "stale.bench.test."}

func (f *networkFixture) preflight(t testing.TB) {
	t.Helper()
	for _, name := range mixedNames {
		m, err := f.exchange(context.Background(), "udp", name, 1, true, 0)
		require.NoError(t, err)
		if name != "stale.bench.test." {
			require.NoError(t, validateMixed(m, name))
		} else {
			require.Len(t, m.Answer, 1)
			require.EqualValues(t, 1, m.Answer[0].Header().Ttl)
		}
	}
	require.EqualValues(t, 3, f.calls.Load(), "warm/negative/stale each forward once; block never forwards")
	// Pipeline uses real time: expire only the one-second stale fixture.
	time.Sleep(1100 * time.Millisecond)
	for i, name := range mixedNames {
		m, err := f.exchange(context.Background(), "udp", name, uint16(i+2), false, 0)
		require.NoError(t, err)
		require.NoError(t, validateMixed(m, name))
	}
	require.EqualValues(t, 3, f.calls.Load(), "RD=0 stale reads must not trigger refresh")
}

func (f *networkFixture) mixedFactory(network string) runner.Factory {
	return func() runner.Handler {
		return func(ctx context.Context, q workload.Query) error {
			name := mixedNames[q.Index%len(mixedNames)]
			m, err := f.exchange(ctx, network, name, uint16(q.Index), false, 0)
			if err != nil {
				return err
			}
			return validateMixed(m, name)
		}
	}
}

func TestNetworkMixedWorkload(t *testing.T) {
	f := newNetworkFixture(t)
	f.preflight(t)
	w, err := workload.New(workload.Spec{Family: "hot-key", Count: 80, Rate: 1000})
	require.NoError(t, err)
	for _, network := range []string{"udp", "tcp"} {
		r, err := runner.Run(context.Background(), w, runner.Options{Workers: 4, Queue: 80, Timeout: time.Second}, f.mixedFactory(network))
		require.NoError(t, err)
		assert.Equal(t, 80, r.Offered)
		assert.Equal(t, map[runner.Outcome]int{runner.Answered: 80}, r.Counts)
	}
	assert.EqualValues(t, 3, f.calls.Load())
	assert.EqualValues(t, 41, f.p.CacheStats().Stale)
	// A recursive stale read returns immediately while one controlled refresh
	// is pending; releasing the fixture clock installs a fresh one-second entry.
	f.delay.Store(true)
	m, err := f.exchange(context.Background(), "udp", "stale.bench.test.", 500, true, 0)
	require.NoError(t, err)
	require.NoError(t, validateMixed(m, "stale.bench.test."))
	require.Eventually(t, func() bool { return f.clock.Pending() == 1 }, time.Second, time.Millisecond)
	assert.EqualValues(t, 4, f.calls.Load())
	f.clock.Advance(time.Second)
	require.Eventually(t, func() bool {
		m, err := f.exchange(context.Background(), "udp", "stale.bench.test.", 501, false, 0)
		return err == nil && len(m.Answer) == 1 && m.Answer[0].Header().Ttl == 1
	}, time.Second, time.Millisecond)
	assert.EqualValues(t, 4, f.calls.Load())
}

func TestNetworkLargeReplyBudgets(t *testing.T) {
	f := newNetworkFixture(t)
	for _, edns := range []uint16{0, 1232} {
		before := f.calls.Load()
		for _, network := range []string{"udp", "udp", "tcp"} {
			m, err := f.exchange(context.Background(), network, "large.bench.test.", 42, true, edns)
			require.NoError(t, err)
			assert.Equal(t, dns.RcodeSuccess, m.Rcode)
			wire, err := m.Pack()
			require.NoError(t, err)
			if network == "udp" {
				budget := 512
				if edns != 0 {
					budget = int(edns)
				}
				assert.LessOrEqual(t, len(wire), budget)
				assert.True(t, m.Truncated)
				assert.Empty(t, m.Answer)
			} else {
				assert.False(t, m.Truncated)
				require.Len(t, m.Answer, 1)
				txt, ok := m.Answer[0].(*dns.TXT)
				require.True(t, ok)
				want := make([]string, 0, 6)
				for _, ch := range "xyzwvu" {
					want = append(want, strings.Repeat(string(ch), 200))
				}
				assert.Equal(t, want, txt.Txt)
				assert.Greater(t, len(wire), 1232)
			}
		}
		assert.EqualValues(t, before+1, f.calls.Load(), "UDP fitting must preserve full cached TCP answer")
	}
}

func TestNetworkCoalescedDelayAndDrop(t *testing.T) {
	f := newNetworkFixture(t)
	f.delay.Store(true)
	errs := make(chan error, 8)
	for i := range 8 {
		go func() {
			m, err := f.exchange(context.Background(), "udp", "coalesced.bench.test.", uint16(i), true, 0)
			if err == nil {
				err = validateMixed(m, "coalesced.bench.test.")
			}
			errs <- err
		}()
	}
	require.Eventually(t, func() bool { return f.p.CacheStats().Misses == 8 && f.clock.Pending() == 1 }, time.Second, time.Millisecond)
	assert.EqualValues(t, 1, f.calls.Load(), "all eight cold arrivals share one delayed upstream")
	f.clock.Advance(time.Second)
	for range 8 {
		require.NoError(t, <-errs)
	}
	assert.EqualValues(t, 1, f.calls.Load())
	f.delay.Store(false)
	f.fail.Store(true)
	m, err := f.exchange(context.Background(), "udp", "failed.bench.test.", 98, true, 0)
	require.NoError(t, err)
	assert.Equal(t, dns.RcodeServerFailure, m.Rcode)
	assert.Empty(t, m.Answer)
	assert.EqualValues(t, 2, f.calls.Load())
	f.fail.Store(false)
	f.drop.Store(true)
	w, err := workload.New(workload.Spec{Family: "hot-key", Count: 1, Rate: 1})
	require.NoError(t, err)
	r, err := runner.Run(context.Background(), w, runner.Options{Workers: 1, Queue: 1, Timeout: 50 * time.Millisecond}, func() runner.Handler {
		return func(ctx context.Context, q workload.Query) error {
			_, err := f.exchange(ctx, "udp", "dropped.bench.test.", 99, true, 0)
			// Wait for the scheduled deadline when the socket deadline rounds down.
			if err != nil {
				<-ctx.Done()
				return ctx.Err()
			}
			return fmt.Errorf("dropped upstream unexpectedly answered")
		}
	})
	require.NoError(t, err)
	assert.Equal(t, map[runner.Outcome]int{runner.Timeout: 1}, r.Counts)
	require.Eventually(t, func() bool { return f.calls.Load() == 3 }, time.Second, time.Millisecond)
}

func BenchmarkNetworkMixed(b *testing.B) {
	f := newNetworkFixture(b)
	f.preflight(b)
	w, err := workload.New(workload.Spec{Family: "hot-key", Count: 100, Rate: 2000})
	require.NoError(b, err)
	latencies := make([]time.Duration, 0, workload.MaxQueries)
	failed := 0
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		r, runErr := runner.Run(context.Background(), w, runner.Options{Workers: 4, Queue: 100, Timeout: time.Second}, f.mixedFactory("udp"))
		if runErr != nil {
			err = runErr
		}
		failed += 100 - r.Counts[runner.Answered]
		for _, s := range r.Samples {
			if len(latencies) < workload.MaxQueries {
				latencies = append(latencies, s.Latency)
			}
		}
	}
	b.StopTimer()
	require.NoError(b, err)
	require.Zero(b, failed)
	require.EqualValues(b, 3, f.calls.Load())
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	b.ReportMetric(float64(latencies[(len(latencies)-1)*50/100].Nanoseconds())/1000, "p50-us")
	b.ReportMetric(float64(latencies[(len(latencies)-1)*99/100].Nanoseconds())/1000, "p99-us")
	b.ReportMetric(100, "queries/op")
	b.ReportMetric(float64(failed), "failed")
	b.ReportMetric(0, "upstream/query")
}
