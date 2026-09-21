package resolve

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/richkeenan/dimsum/internal/config"
	"github.com/richkeenan/dimsum/internal/dnscache"
	"github.com/richkeenan/dimsum/internal/dnswire"
	"github.com/richkeenan/dimsum/internal/testutil"
	"github.com/richkeenan/dimsum/internal/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func stalePipeline(t *testing.T, mode string, handler func(testutil.Request) testutil.Response) (*Pipeline, *transport.Request, string) {
	t.Helper()
	u, err := testutil.NewUpstream(testutil.NewClock(time.Now()), handler)
	require.NoError(t, err)
	t.Cleanup(func() { u.Close() })
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	text := fmt.Sprintf("version: 1\ndns:\n  listen: [127.0.0.1:0]\n  upstreams: [%s]\nadmin:\n  listen: 127.0.0.1:0\npaths:\n  data_dir: %s/data\n  secrets_dir: %s/secrets\ncache:\n  stale_mode: %s\n", u.Address(), dir, dir, mode)
	require.NoError(t, os.WriteFile(path, []byte(text), 0600))
	s, err := config.OpenStore(context.Background(), path, filepath.Join(dir, "state"), config.StoreOptions{})
	require.NoError(t, err)
	p := NewWithStore(nil, s)
	t.Cleanup(func() { require.NoError(t, p.Close()) })
	q := new(dns.Msg)
	q.SetQuestion("Stale.example.", dns.TypeA)
	q.Id = 42
	wire, err := q.Pack()
	require.NoError(t, err)
	r := &transport.Request{Wire: wire}
	require.NoError(t, dnswire.ParseRequest(wire, &r.Message))
	return p, r, path
}

func seedStale(t *testing.T, p *Pipeline, r *transport.Request, age time.Duration) {
	t.Helper()
	var q dns.Msg
	require.NoError(t, q.Unpack(r.Wire))
	m := new(dns.Msg)
	m.SetReply(&q)
	m.AuthenticatedData = true
	m.Answer = []dns.RR{&dns.A{Hdr: dns.RR_Header{Name: q.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60}, A: net.ParseIP("192.0.2.10")}}
	wire, err := m.Pack()
	require.NoError(t, err)
	c, _, err := p.cacheFor(p.store.Snapshot())
	require.NoError(t, err)
	k, ok := cacheKey(r, p.store.Snapshot())
	require.True(t, ok)
	require.True(t, c.Put(k, wire, time.Now().Add(-60*time.Second-age)))
}

func TestStaleModesAndMaximumAge(t *testing.T) {
	for _, mode := range []string{"immediate", "failure-only", "off"} {
		t.Run(mode, func(t *testing.T) {
			var exchanges atomic.Int32
			p, r, _ := stalePipeline(t, mode, func(req testutil.Request) testutil.Response {
				exchanges.Add(1)
				wire := append([]byte(nil), req.Wire...)
				wire[2] |= 0x80
				wire[3] = (wire[3] & 0xf0) | 2
				return testutil.Response{Wire: wire}
			})
			seedStale(t, p, r, time.Second)
			out := make([]byte, 65535)
			n, err := p.Resolve(context.Background(), r, out)
			var got dns.Msg
			if mode == "off" {
				assert.Error(t, err)
				assert.Zero(t, n)
			} else {
				require.NoError(t, err)
				require.NoError(t, got.Unpack(out[:n]))
				require.Len(t, got.Answer, 1)
				assert.EqualValues(t, 30, got.Answer[0].Header().Ttl)
				assert.False(t, got.AuthenticatedData)
				assert.EqualValues(t, 42, got.Id)
			}
			require.Eventually(t, func() bool { return exchanges.Load() == 1 }, time.Second, time.Millisecond)
			// Join any immediate refresh before replacing the seed.
			p.cache.flights.mu.Lock()
			var done <-chan struct{}
			for _, f := range p.cache.flights.active {
				done = f.done
			}
			p.cache.flights.mu.Unlock()
			if done != nil {
				<-done
			}
			seedStale(t, p, r, 3601*time.Second)
			n, err = p.Resolve(context.Background(), r, out)
			assert.Error(t, err)
			assert.Zero(t, n)
		})
	}
}

func TestImmediateRefreshDeduplicatedAndRetiredGeneration(t *testing.T) {
	entered, release := make(chan struct{}, 1), make(chan struct{})
	p, r, path := stalePipeline(t, "immediate", func(req testutil.Request) testutil.Response {
		entered <- struct{}{}
		<-release
		wire := append([]byte(nil), req.Wire...)
		wire[2] |= 0x80
		wire[7] = 1
		wire = append(wire, 0xc0, 12, 0, 1, 0, 1, 0, 0, 0, 60, 0, 4, 192, 0, 2, 20)
		return testutil.Response{Wire: wire}
	})
	// Release even when a prerequisite fails, before Pipeline.Close waits.
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	seedStale(t, p, r, time.Second)
	out := make([]byte, 65535)
	for range 10 {
		n, err := p.Resolve(context.Background(), r, out)
		require.NoError(t, err)
		var m dns.Msg
		require.NoError(t, m.Unpack(out[:n]))
		assert.Equal(t, "192.0.2.10", m.Answer[0].(*dns.A).A.String())
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("refresh did not start")
	}
	assert.EqualValues(t, 10, p.CacheStats().Stale)
	assert.Len(t, entered, 0)
	old := p.store.Snapshot()
	// A new generation must not see the old cache entry or its eventual refresh.
	text, err := os.ReadFile(path)
	require.NoError(t, err)
	text = append(text, []byte("rules:\n  - {id: deny, kind: exact, action: deny, pattern: unrelated.example, enabled: true}\n")...)
	require.NoError(t, os.WriteFile(path, text, 0600))
	_, err = p.store.Reload(context.Background())
	require.NoError(t, err)
	require.Greater(t, p.store.Snapshot().Generation(), old.Generation())
	close(release)
	released = true
	require.Eventually(t, func() bool { return p.CacheStats().RefreshSuccess == 1 }, time.Second, time.Millisecond)
	k, ok := cacheKey(r, old)
	require.True(t, ok)
	c, _, err := p.cacheFor(old)
	require.NoError(t, err)
	other, ok := dnscache.NewKey(&r.Message, 1, p.store.Snapshot().Generation(), 0)
	require.True(t, ok)
	assert.NotEqual(t, k, other)
	assert.False(t, c.Lookup(other, &r.Message, out, time.Now(), 3600, 30).Hit)
	hit := c.Lookup(k, &r.Message, out, time.Now(), 3600, 30)
	require.True(t, hit.Hit)
	assert.False(t, hit.Stale)
}

func TestTenCoalescedClients(t *testing.T) {
	release := make(chan struct{})
	var calls atomic.Int32
	p, r, _ := stalePipeline(t, "off", func(req testutil.Request) testutil.Response {
		calls.Add(1)
		<-release
		wire := append([]byte(nil), req.Wire...)
		wire[2] |= 0x80
		wire[7] = 1
		wire = append(wire, 0xc0, 12, 0, 1, 0, 1, 0, 0, 0, 60, 0, 4, 192, 0, 2, 20)
		return testutil.Response{Wire: wire}
	})
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	type result struct {
		id       int
		wire     []byte
		err      error
		metadata transport.Result
	}
	results := make(chan result, 10)
	for i := range 10 {
		wire := append([]byte(nil), r.Wire...)
		wire[1] = byte(i)
		if i%2 == 0 {
			wire[13] = 's'
		}
		req := transport.Request{Wire: wire}
		require.NoError(t, dnswire.ParseRequest(wire, &req.Message))
		go func() {
			out := make([]byte, 65535)
			n, err := p.Resolve(context.Background(), &req, out)
			results <- result{i, out[:n], err, req.Result}
		}()
	}
	require.Eventually(t, func() bool {
		p.cache.flights.mu.Lock()
		defer p.cache.flights.mu.Unlock()
		for _, f := range p.cache.flights.active {
			if f.waiters == 10 {
				return true
			}
		}
		return false
	}, time.Second, time.Millisecond)
	close(release)
	released = true
	coalesced := 0
	for range 10 {
		result := <-results
		require.NoError(t, result.err)
		var m dns.Msg
		require.NoError(t, m.Unpack(result.wire))
		assert.EqualValues(t, result.id, m.Id)
		want := "Stale.example."
		if result.id%2 == 0 {
			want = "stale.example."
		}
		assert.Equal(t, want, m.Question[0].Name)
		assert.False(t, m.AuthenticatedData)
		assert.Equal(t, transport.ForwardedAnswer, result.metadata.Outcome)
		assert.EqualValues(t, 1, result.metadata.UpstreamID)
		if result.metadata.Coalesced {
			coalesced++
		}
	}
	assert.EqualValues(t, 1, calls.Load())
	assert.Equal(t, 9, coalesced)
}

func TestRefreshOverflowStillServesStale(t *testing.T) {
	p, r, _ := stalePipeline(t, "immediate", func(req testutil.Request) testutil.Response { return testutil.Response{Drop: true} })
	seedStale(t, p, r, time.Second)
	for i := range maxRefresh {
		k, ok := dnscache.NewKey(&r.Message, uint64(i+100), 0, 0)
		require.True(t, ok)
		_, err := p.cache.flights.join(k, true, func(ctx context.Context) ([]byte, error) { <-ctx.Done(); return nil, ctx.Err() })
		require.NoError(t, err)
	}
	out := make([]byte, 65535)
	n, err := p.Resolve(context.Background(), r, out)
	require.NoError(t, err)
	var m dns.Msg
	require.NoError(t, m.Unpack(out[:n]))
	require.Len(t, m.Answer, 1)
	assert.EqualValues(t, 30, m.Answer[0].Header().Ttl)
	assert.EqualValues(t, 1, p.CacheStats().Overflow)
}
