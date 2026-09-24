package resolve

import (
	"context"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/richkeenan/dimsum/internal/dnswire"
	"github.com/richkeenan/dimsum/internal/testutil"
	"github.com/richkeenan/dimsum/internal/transport"
	"github.com/richkeenan/dimsum/internal/upstream"
	"github.com/stretchr/testify/require"
)

// Each operation is ten concurrent clients against a loopback upstream with an
// injected 1 ms delay. Record-free responses deliberately bypass cache admission
// so this measures coalescing rather than a warmed fresh hit.
func BenchmarkCoalescedClients(b *testing.B) {
	var exchanges atomic.Int64
	u, err := testutil.NewUpstream(testutil.NewClock(time.Now()), func(req testutil.Request) testutil.Response {
		exchanges.Add(1)
		time.Sleep(time.Millisecond)
		wire := append([]byte(nil), req.Wire...)
		wire[2] |= 0x80
		return testutil.Response{Wire: wire}
	})
	require.NoError(b, err)
	defer u.Close()
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			select {
			case <-u.Requests():
			case <-done:
				return
			}
		}
	}()
	c, err := upstream.New(upstream.Options{Endpoints: []upstream.Endpoint{upstream.PlainEndpoint(netip.MustParseAddrPort(u.Address()))}})
	require.NoError(b, err)
	p := New(c)
	defer p.Close()
	_, _, err = p.cacheFor(nil)
	require.NoError(b, err)
	var requests [10]transport.Request
	for i := range requests {
		wire := []byte{0, byte(i), 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 1, 'a', 0, 0, 1, 0, 1}
		requests[i].Wire = wire
		require.NoError(b, dnswire.ParseRequest(wire, &requests[i].Message))
	}
	type result struct {
		duration time.Duration
		valid    bool
	}
	results := make(chan result, 10)
	var latency time.Duration
	failures := 0
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		for i := range requests {
			go func() {
				out := make([]byte, 65535)
				start := time.Now()
				n, err := p.Resolve(context.Background(), &requests[i], out)
				results <- result{time.Since(start), err == nil && n >= 12 && out[1] == byte(i)}
			}()
		}
		for range requests {
			r := <-results
			latency += r.duration
			if !r.valid {
				failures++
			}
		}
	}
	b.StopTimer()
	require.Zero(b, failures)
	b.ReportMetric(float64(exchanges.Load())/float64(10*b.N), "upstream/client")
	b.ReportMetric(float64(latency.Nanoseconds())/float64(10*b.N), "client-ns/answer")
}

// Exercises real upstream I/O on every operation. Record-free replies deliberately
// bypass caching, so per-miss allocation costs cannot disappear into cache hits.
func BenchmarkUncachedPipeline(b *testing.B) {
	u, err := testutil.NewUpstream(testutil.NewClock(time.Now()), func(req testutil.Request) testutil.Response {
		wire := append([]byte(nil), req.Wire...)
		wire[2] |= 0x80
		return testutil.Response{Wire: wire}
	})
	require.NoError(b, err)
	b.Cleanup(func() { require.NoError(b, u.Close()) })
	done := make(chan struct{})
	b.Cleanup(func() { close(done) })
	go func() {
		for {
			select {
			case <-u.Requests():
			case <-done:
				return
			}
		}
	}()
	c, err := upstream.New(upstream.Options{Endpoints: []upstream.Endpoint{upstream.PlainEndpoint(netip.MustParseAddrPort(u.Address()))}})
	require.NoError(b, err)
	p := New(c)
	b.Cleanup(func() { require.NoError(b, p.Close()) })
	_, _, err = p.cacheFor(nil)
	require.NoError(b, err)
	r := transport.Request{Wire: []byte{0, 1, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 1, 'a', 0, 0, 1, 0, 1}}
	require.NoError(b, dnswire.ParseRequest(r.Wire, &r.Message))
	out := make([]byte, 65535)
	var n int
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		n, err = p.Resolve(context.Background(), &r, out)
		if err != nil {
			break
		}
	}
	b.StopTimer()
	require.NoError(b, err)
	require.Equal(b, len(r.Wire), n)
}
