package resolve_test

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/richkeenan/dimsum/internal/resolve"
	"github.com/richkeenan/dimsum/internal/testutil"
	"github.com/richkeenan/dimsum/internal/transport"
	"github.com/richkeenan/dimsum/internal/upstream"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCacheTransportReplyBudgets(t *testing.T) {
	for _, tc := range []struct {
		name      string
		edns      bool
		chunks    int
		budget    int
		truncated bool
	}{
		{name: "no_EDNS_512", chunks: 8, budget: 512, truncated: true},
		{name: "EDNS_1232_fits", edns: true, chunks: 3, budget: 1232},
		{name: "EDNS_1232_truncated", edns: true, chunks: 8, budget: 1232, truncated: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text := make([]string, tc.chunks)
			for i := range text {
				text[i] = strings.Repeat(string(rune('a'+i)), 200)
			}
			var calls atomic.Int32
			workerErrors := make(chan error, 16)
			u, err := testutil.NewUpstream(testutil.NewClock(time.Now()), func(r testutil.Request) testutil.Response {
				calls.Add(1)
				var q dns.Msg
				if err := q.Unpack(r.Wire); err != nil {
					workerErrors <- err
					return testutil.Response{Drop: true}
				}
				reply := new(dns.Msg)
				reply.SetReply(&q)
				reply.RecursionAvailable = true
				reply.AuthenticatedData = true
				reply.Answer = []dns.RR{&dns.TXT{
					Hdr: dns.RR_Header{Name: q.Question[0].Name, Rrtype: dns.TypeTXT, Class: dns.ClassINET, Ttl: 60},
					Txt: text,
				}}
				if q.IsEdns0() != nil {
					reply.SetEdns0(1232, false)
				}
				wire, err := reply.Pack()
				if err != nil {
					workerErrors <- err
				}
				return testutil.Response{Wire: wire}
			})
			require.NoError(t, err)
			t.Cleanup(func() {
				assert.NoError(t, u.Close())
				close(workerErrors)
				for err := range workerErrors {
					assert.NoError(t, err)
				}
			})
			client, err := upstream.New(upstream.Options{Endpoints: []upstream.Endpoint{upstream.PlainEndpoint(netip.MustParseAddrPort(u.Address()))}})
			require.NoError(t, err)
			pipeline := resolve.New(client)
			t.Cleanup(func() { assert.NoError(t, pipeline.Close()) })
			addr := cacheTransportListener(t, pipeline)

			q := new(dns.Msg)
			q.SetQuestion("LaRgE.example.", dns.TypeTXT)
			q.Id = 101
			if tc.edns {
				q.SetEdns0(1232, false)
			}
			checkUDP := func(got *dns.Msg, size int) {
				assert.LessOrEqual(t, size, tc.budget)
				assert.Equal(t, tc.truncated, got.Truncated)
				if tc.truncated {
					assert.Empty(t, got.Answer, "transport should emit a question-only TC reply")
				} else {
					assert.Greater(t, size, 512, "EDNS must permit an answer exceeding the legacy budget")
					require.Len(t, got.Answer, 1)
					txt, ok := got.Answer[0].(*dns.TXT)
					require.True(t, ok)
					assert.Equal(t, text, txt.Txt)
				}
				if tc.edns {
					require.NotNil(t, got.IsEdns0())
					assert.EqualValues(t, 1232, got.IsEdns0().UDPSize())
				} else {
					assert.Nil(t, got.IsEdns0())
				}
			}

			// The first UDP reply may be truncated, but the cache must retain
			// the complete upstream message rather than that fitted reply.
			got, size := cacheTransportExchange(t, "udp", addr, q)
			checkUDP(got, size)
			assert.EqualValues(t, 1, calls.Load())
			assert.EqualValues(t, 1, pipeline.CacheStats().Misses)
			assert.Zero(t, pipeline.CacheStats().Hits)

			q.Id = 202
			q.Question[0].Name = "large.EXAMPLE."
			got, size = cacheTransportExchange(t, "udp", addr, q)
			checkUDP(got, size)
			assert.EqualValues(t, 1, pipeline.CacheStats().Hits)
			assert.EqualValues(t, 1, calls.Load(), "UDP retry must be a cache hit")

			// TCP shares the UDP cache key, including the no-EDNS variant.
			q.Id = 303
			got, size = cacheTransportExchange(t, "tcp", addr, q)
			assert.False(t, got.Truncated)
			assert.Greater(t, size, 512)
			if tc.truncated {
				assert.Greater(t, size, tc.budget)
			}
			require.Len(t, got.Answer, 1)
			txt, ok := got.Answer[0].(*dns.TXT)
			require.True(t, ok)
			assert.Equal(t, text, txt.Txt, "UDP fitting must not damage the cached full answer")
			assert.EqualValues(t, 2, pipeline.CacheStats().Hits)
			assert.EqualValues(t, 1, calls.Load(), "TCP must reuse the UDP-populated entry")

			q.Id = 404
			got, size = cacheTransportExchange(t, "udp", addr, q)
			checkUDP(got, size)
			stats := pipeline.CacheStats()
			assert.EqualValues(t, 3, stats.Hits)
			assert.EqualValues(t, 1, stats.Misses)
			assert.Zero(t, stats.Bypasses)
			assert.Zero(t, stats.Stale)
			assert.EqualValues(t, 1, calls.Load())
		})
	}
}

func cacheTransportListener(t *testing.T, pipeline *resolve.Pipeline) string {
	t.Helper()
	tcp, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { tcp.Close() })
	udp, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: tcp.Addr().(*net.TCPAddr).Port})
	require.NoError(t, err)
	t.Cleanup(func() { udp.Close() })
	s, err := transport.New(transport.Options{SmallSlots: 8, LargeSlots: 1, Workers: 1, MaxConnections: 2}, pipeline)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 2)
	go func() { done <- s.ServeUDP(ctx, udp) }()
	go func() { done <- s.ServeTCP(ctx, tcp) }()
	t.Cleanup(func() {
		cancel()
		for range 2 {
			select {
			case err := <-done:
				assert.NoError(t, err)
			case <-time.After(3 * time.Second):
				assert.Fail(t, "transport failed to stop")
			}
		}
	})
	return tcp.Addr().String()
}

// Raw sockets avoid automatic TCP fallback and measure the actual UDP payload.
func cacheTransportExchange(t *testing.T, network, addr string, q *dns.Msg) (*dns.Msg, int) {
	t.Helper()
	wire, err := q.Pack()
	require.NoError(t, err)
	c, err := net.DialTimeout(network, addr, time.Second)
	require.NoError(t, err)
	defer c.Close()
	require.NoError(t, c.SetDeadline(time.Now().Add(2*time.Second)))
	if network == "tcp" {
		frame := make([]byte, len(wire)+2)
		binary.BigEndian.PutUint16(frame, uint16(len(wire)))
		copy(frame[2:], wire)
		wire = frame
	}
	n, err := c.Write(wire)
	require.NoError(t, err)
	require.Equal(t, len(wire), n)
	buf := make([]byte, 65535)
	if network == "tcp" {
		var prefix [2]byte
		_, err = io.ReadFull(c, prefix[:])
		require.NoError(t, err)
		n = int(binary.BigEndian.Uint16(prefix[:]))
		_, err = io.ReadFull(c, buf[:n])
	} else {
		n, err = c.Read(buf)
	}
	require.NoError(t, err)
	got := new(dns.Msg)
	require.NoError(t, got.Unpack(buf[:n]))
	assert.Equal(t, dns.RcodeSuccess, got.Rcode)
	assert.True(t, got.Response)
	assert.True(t, got.RecursionAvailable)
	assert.Equal(t, q.Id, got.Id)
	assert.Equal(t, q.Question, got.Question)
	assert.Equal(t, q.RecursionDesired, got.RecursionDesired)
	assert.False(t, got.AuthenticatedData)
	return got, n
}
