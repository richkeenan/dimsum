package dhcp

import (
	"context"
	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type input struct {
	wire      []byte
	truncated bool
}
type testConn struct {
	input  chan input
	closed chan struct{}
	once   sync.Once
}

func newTestConn() *testConn { return &testConn{input: make(chan input), closed: make(chan struct{})} }
func (c *testConn) Receive(b []byte) (int, netip.AddrPort, bool, error) {
	select {
	case p := <-c.input:
		return copy(b, p.wire), netip.MustParseAddrPort("192.0.2.10:68"), p.truncated, nil
	case <-c.closed:
		return 0, netip.AddrPort{}, false, net.ErrClosed
	}
}
func (c *testConn) Close() error { c.once.Do(func() { close(c.closed) }); return nil }
func discover() []byte {
	b := make([]byte, 244)
	b[0], b[1], b[2], b[4] = 1, 1, 6, 42
	copy(b[28:34], []byte{2, 0, 0, 0, 0, 1})
	copy(b[236:], []byte{99, 130, 83, 99, 53, 1, 1, 255})
	return b
}
func startTransport(t *testing.T, c *testConn, clock func() time.Time, h Handler) *Transport {
	t.Helper()
	r, err := NewTransport(c, h, clock)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("transport failed to stop")
		}
	})
	return r
}

// Removing admission before decoding or adding a worker per packet breaks this.
func TestTransportBoundedAdmission(t *testing.T) {
	c := newTestConn()
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	var calls atomic.Int64
	r := startTransport(t, c, func() time.Time { return time.Unix(1, 0) }, func(ctx context.Context, p *dhcpv4.DHCPv4, peer netip.AddrPort) {
		calls.Add(1)
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-release:
		case <-ctx.Done():
		}
	})
	c.input <- input{wire: discover()}
	<-entered
	for range 1000 {
		c.input <- input{wire: discover()}
	}
	require.Eventually(t, func() bool { return r.Stats().RateLimited == 937 }, time.Second, time.Millisecond)
	s := r.Stats()
	assert.EqualValues(t, 1, calls.Load())
	assert.EqualValues(t, 33, s.Admitted)
	assert.EqualValues(t, 31, s.QueueFull)
	assert.EqualValues(t, 937, s.RateLimited)
	close(release)
	require.Eventually(t, func() bool { return calls.Load() == 33 }, time.Second, time.Millisecond)
}

func TestTransportRejectsOversizeTruncationAndMalformed(t *testing.T) {
	c := newTestConn()
	got := make(chan byte, 4)
	r := startTransport(t, c, time.Now, func(_ context.Context, p *dhcpv4.DHCPv4, _ netip.AddrPort) { got <- p.TransactionID[0] })
	large := append(discover(), make([]byte, 65507-244)...)
	c.input <- input{wire: large}
	c.input <- input{wire: discover(), truncated: true}
	c.input <- input{wire: []byte{1}}
	good := discover()
	good[4] = 99
	c.input <- input{wire: good}
	select {
	case id := <-got:
		assert.EqualValues(t, 99, id)
	case <-time.After(time.Second):
		t.Fatal("valid request not handled")
	}
	s := r.Stats()
	assert.EqualValues(t, 2, s.Oversized)
	assert.EqualValues(t, 1, s.Malformed)
	assert.EqualValues(t, 2, s.Admitted)
}

func TestTransportRefillsAdmission(t *testing.T) {
	c := newTestConn()
	var now atomic.Int64
	now.Store(time.Second.Nanoseconds())
	var calls atomic.Int64
	r := startTransport(t, c, func() time.Time { return time.Unix(0, now.Load()) }, func(context.Context, *dhcpv4.DHCPv4, netip.AddrPort) { calls.Add(1) })
	for i := int64(1); i <= 64; i++ {
		c.input <- input{wire: discover()}
		require.Eventually(t, func() bool { return calls.Load() == i }, time.Second, time.Millisecond)
	}
	c.input <- input{wire: discover()}
	require.Eventually(t, func() bool { return r.Stats().RateLimited == 1 }, time.Second, time.Millisecond)
	now.Add(int64(5 * time.Millisecond))
	c.input <- input{wire: discover()}
	require.Eventually(t, func() bool { return calls.Load() == 65 }, time.Second, time.Millisecond)
}

func TestTransportCloseUnblocksReceive(t *testing.T) {
	c := newTestConn()
	r, err := NewTransport(c, func(context.Context, *dhcpv4.DHCPv4, netip.AddrPort) {}, time.Now)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	cancel()
	select {
	case err := <-done:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("receive blocked shutdown")
	}
	assert.Error(t, r.Run(context.Background()))
}

func TestTransportReadError(t *testing.T) {
	c := newTestConn()
	require.NoError(t, c.Close())
	r, err := NewTransport(c, func(context.Context, *dhcpv4.DHCPv4, netip.AddrPort) {}, nil)
	require.NoError(t, err)
	assert.ErrorIs(t, r.Run(context.Background()), net.ErrClosed)
}

func TestTransportAcceptsMaximumDatagram(t *testing.T) {
	c := newTestConn()
	got := make(chan struct{}, 1)
	r := startTransport(t, c, nil, func(context.Context, *dhcpv4.DHCPv4, netip.AddrPort) { got <- struct{}{} })
	c.input <- input{wire: append(discover(), make([]byte, 4096-244)...)}
	select {
	case <-got:
	case <-time.After(time.Second):
		t.Fatal("maximum-size packet rejected")
	}
	assert.Zero(t, r.Stats().Oversized)
}
