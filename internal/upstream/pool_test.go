package upstream_test

import (
	"context"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/richkeenan/dimsum/internal/testutil"
	"github.com/richkeenan/dimsum/internal/upstream"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func poolFixture(t *testing.T, handler func(testutil.Request) testutil.Response) *testutil.Upstream {
	t.Helper()
	u, err := testutil.NewUpstream(testutil.NewClock(time.Now()), handler)
	require.NoError(t, err)
	t.Cleanup(func() { u.Close() })
	return u
}

func TestPrimaryFallbackOutcomes(t *testing.T) {
	for _, code := range []int{0, 2, 3, 5, -1} {
		t.Run(string(rune('A'+code+1)), func(t *testing.T) {
			primary := poolFixture(t, func(r testutil.Request) testutil.Response {
				if code < 0 {
					return testutil.Response{Drop: true}
				}
				p := answer(r.Wire)
				p[3] = byte(code)
				return testutil.Response{Wire: p}
			})
			fallback := poolFixture(t, func(r testutil.Request) testutil.Response { return testutil.Response{Wire: answer(r.Wire)} })
			c, err := upstream.New(upstream.Options{Endpoints: []netip.AddrPort{netip.MustParseAddrPort(primary.Address())}, Fallback: []netip.AddrPort{netip.MustParseAddrPort(fallback.Address())}, AttemptTimeout: 20 * time.Millisecond})
			require.NoError(t, err)
			result, err := c.Exchange(context.Background(), query(), make([]byte, 65535))
			require.NoError(t, err)
			want := primary.Address()
			attempts := 1
			if code == 2 || code == 5 || code < 0 {
				want = fallback.Address()
				attempts = 2
			}
			assert.Equal(t, want, result.Endpoint.String())
			assert.Equal(t, attempts, result.Attempts)
			assert.Equal(t, uint64(1), c.Health()[0].Responses+c.Health()[0].Failures)
		})
	}
}

func TestCircuitRecoveryAndCancellation(t *testing.T) {
	var healthy atomic.Bool
	primary := poolFixture(t, func(r testutil.Request) testutil.Response {
		if !healthy.Load() {
			return testutil.Response{Drop: true}
		}
		return testutil.Response{Wire: answer(r.Wire)}
	})
	fallback := poolFixture(t, func(r testutil.Request) testutil.Response { return testutil.Response{Wire: answer(r.Wire)} })
	c, err := upstream.New(upstream.Options{Endpoints: []netip.AddrPort{netip.MustParseAddrPort(primary.Address())}, Fallback: []netip.AddrPort{netip.MustParseAddrPort(fallback.Address())}, AttemptTimeout: 10 * time.Millisecond, OpenInterval: 40 * time.Millisecond})
	require.NoError(t, err)
	for range 2 {
		_, err = c.Exchange(context.Background(), query(), make([]byte, 65535))
		require.NoError(t, err)
	}
	assert.Equal(t, "open", c.Health()[0].State)
	r, err := c.Exchange(context.Background(), query(), make([]byte, 65535))
	require.NoError(t, err)
	assert.Equal(t, 1, r.Attempts)
	assert.Len(t, primary.Requests(), 2)
	healthy.Store(true)
	require.Eventually(t, func() bool { return time.Now().After(c.Health()[0].RetryAt) }, time.Second, time.Millisecond)
	r, err = c.Exchange(context.Background(), query(), make([]byte, 65535))
	require.NoError(t, err)
	assert.Equal(t, primary.Address(), r.Endpoint.String())
	assert.Equal(t, "closed", c.Health()[0].State)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = c.Exchange(ctx, query(), make([]byte, 65535))
	assert.ErrorIs(t, err, context.Canceled)
	assert.Zero(t, c.Outstanding())
}

func TestPoolBudget(t *testing.T) {
	u := poolFixture(t, func(testutil.Request) testutil.Response { return testutil.Response{Drop: true} })
	ep := netip.MustParseAddrPort(u.Address())
	c, err := upstream.New(upstream.Options{Endpoints: []netip.AddrPort{ep, ep}, Fallback: []netip.AddrPort{ep}, MaxAttempts: 1, AttemptTimeout: 10 * time.Millisecond})
	require.NoError(t, err)
	r, err := c.Exchange(context.Background(), query(), make([]byte, 65535))
	assert.Error(t, err)
	assert.Equal(t, 1, r.Attempts)
	assert.Len(t, u.Requests(), 1)
	c, err = upstream.New(upstream.Options{Endpoints: []netip.AddrPort{ep, ep}, Fallback: []netip.AddrPort{ep}})
	require.NoError(t, err)
	start := time.Now()
	r, err = c.Exchange(context.Background(), query(), make([]byte, 65535))
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Equal(t, 3, r.Attempts)
	assert.Less(t, time.Since(start), 2300*time.Millisecond)
}

func TestRouteNamespaceDoesNotEscapeDefaultPool(t *testing.T) {
	u := poolFixture(t, func(r testutil.Request) testutil.Response { return testutil.Response{Wire: answer(r.Wire)} })
	c := client(t, u.Address(), time.Second)
	_, err := c.ExchangeRoute(context.Background(), upstream.RouteKey(2), query(), make([]byte, 65535))
	assert.ErrorIs(t, err, upstream.ErrRoute)
	assert.Empty(t, u.Requests())
	r, err := c.ExchangeRoute(context.Background(), upstream.DefaultRoute, query(), make([]byte, 65535))
	require.NoError(t, err)
	assert.Equal(t, upstream.DefaultRoute, r.Route)
}

func TestAdaptiveSamplesPrimaryBeforeFallback(t *testing.T) {
	handler := func(r testutil.Request) testutil.Response { return testutil.Response{Wire: answer(r.Wire)} }
	a, b, f := poolFixture(t, handler), poolFixture(t, handler), poolFixture(t, handler)
	c, err := upstream.New(upstream.Options{Mode: "adaptive", Endpoints: []netip.AddrPort{netip.MustParseAddrPort(a.Address()), netip.MustParseAddrPort(b.Address())}, Fallback: []netip.AddrPort{netip.MustParseAddrPort(f.Address())}})
	require.NoError(t, err)
	defer c.Close()
	for _, endpoint := range []string{a.Address(), b.Address()} {
		r, err := c.Exchange(context.Background(), query(), make([]byte, 65535))
		require.NoError(t, err)
		assert.Equal(t, endpoint, r.Endpoint.String())
	}
	assert.Empty(t, f.Requests())
}

func BenchmarkPoolExchange(b *testing.B) {
	for _, tcp := range []bool{false, true} {
		name := "udp"
		if tcp {
			name = "udp-tc-tcp-reused"
		}
		b.Run(name, func(b *testing.B) {
			u, err := testutil.NewUpstream(testutil.NewClock(time.Now()), func(r testutil.Request) testutil.Response {
				p := answer(r.Wire)
				if tcp && r.Network == "udp" {
					p[2] |= 2
				}
				return testutil.Response{Wire: p}
			})
			require.NoError(b, err)
			defer u.Close()
			c, err := upstream.New(upstream.Options{Endpoints: []netip.AddrPort{netip.MustParseAddrPort(u.Address())}})
			require.NoError(b, err)
			defer c.Close()
			wire, out := query(), make([]byte, 65535)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, err = c.Exchange(context.Background(), wire, out)
				if err != nil {
					break
				}
				<-u.Requests()
				if tcp {
					<-u.Requests()
				}
			}
			b.StopTimer()
			require.NoError(b, err)
		})
	}
}
