package dhcp

import (
	"context"
	"net/netip"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func BenchmarkParseReply(b *testing.B) {
	wire := discover()
	var err error
	var out []byte
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		var p, r *dhcpv4.DHCPv4
		p, err = dhcpv4.FromBytes(wire)
		if err != nil {
			break
		}
		r, err = dhcpv4.NewReplyFromRequest(p, dhcpv4.WithMessageType(dhcpv4.MessageTypeOffer))
		if err != nil {
			break
		}
		out = r.ToBytes()
	}
	b.StopTimer()
	require.NoError(b, err)
	require.NotEmpty(b, out)
}

// The handler stays blocked while input grows. Additional per-packet owners,
// workers or retained buffers would grow goroutines/heap between these samples.
func TestTransportOverloadPlateau(t *testing.T) {
	c := newTestConn()
	entered := make(chan struct{})
	r := startTransport(t, c, func() time.Time { return time.Unix(1, 0) }, func(ctx context.Context, _ *dhcpv4.DHCPv4, _ netip.AddrPort) { close(entered); <-ctx.Done() })
	wire := discover()
	c.input <- input{wire: wire}
	<-entered
	sample := func(count int) (uint64, int) {
		for range count {
			c.input <- input{wire: wire}
		}
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		return m.HeapAlloc, runtime.NumGoroutine()
	}
	heap1, g1 := sample(10000)
	heap2, g2 := sample(100000)
	assert.LessOrEqual(t, g2, g1)
	assert.LessOrEqual(t, int64(heap2)-int64(heap1), int64(256<<10))
	assert.EqualValues(t, 33, r.Stats().Admitted)
	t.Logf("10k -> 110k excess input: retained heap %d -> %d bytes; goroutines %d -> %d; counters %+v", heap1, heap2, g1, g2, r.Stats())
}

func BenchmarkOverload(b *testing.B) {
	c := newTestConn()
	entered := make(chan struct{})
	r, err := NewTransport(c, func(ctx context.Context, _ *dhcpv4.DHCPv4, _ netip.AddrPort) { close(entered); <-ctx.Done() }, func() time.Time { return time.Unix(1, 0) })
	require.NoError(b, err)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	wire := discover()
	c.input <- input{wire: wire}
	<-entered
	for range 1000 {
		c.input <- input{wire: wire}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		c.input <- input{wire: wire}
	}
	b.StopTimer()
	cancel()
	require.ErrorIs(b, <-done, context.Canceled)
}

func FuzzTransportIngress(f *testing.F) {
	f.Add(discover(), false)
	f.Add([]byte{1}, false)
	f.Add(discover(), true)
	f.Fuzz(func(t *testing.T, wire []byte, truncated bool) {
		c := newTestConn()
		var processed atomic.Uint64
		r := startTransport(t, c, time.Now, func(context.Context, *dhcpv4.DHCPv4, netip.AddrPort) { processed.Add(1) })
		c.input <- input{wire: wire, truncated: truncated}
		// Fence with a valid packet: processing is serial, so the fence either fills
		// the second result or is the sole result. No parser-only fuzz shortcut.
		c.input <- input{wire: discover()}
		want := uint64(2)
		if truncated || len(wire) > 4096 {
			want = 1
		}
		require.Eventually(t, func() bool { return processed.Load()+r.Stats().Malformed == want }, time.Second, time.Millisecond)
		if truncated || len(wire) > 4096 {
			assert.EqualValues(t, 1, r.Stats().Oversized)
		}
	})
}
