package dhcp

import (
	"context"
	"errors"
	"net/netip"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type runtimeLink struct {
	input  chan []byte
	output chan WireReply
	stop   chan struct{}
	once   sync.Once
}

func newRuntimeLink() *runtimeLink {
	return &runtimeLink{make(chan []byte, 32), make(chan WireReply, 32), make(chan struct{}), sync.Once{}}
}
func (l *runtimeLink) Receive(b []byte) (int, netip.AddrPort, bool, error) {
	select {
	case p := <-l.input:
		return copy(b, p), netip.MustParseAddrPort("0.0.0.0:68"), false, nil
	case <-l.stop:
		return 0, netip.AddrPort{}, false, errors.New("closed")
	}
}
func (l *runtimeLink) Close() error { l.once.Do(func() { close(l.stop) }); return nil }
func (l *runtimeLink) Send(ctx context.Context, w WireReply) error {
	select {
	case l.output <- w:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (l *runtimeLink) MTU() int                            { return 1500 }
func quietProbe(context.Context, netip.Addr) (bool, error) { return false, nil }
func wireReceive(t *testing.T, l *runtimeLink) *dhcpv4.DHCPv4 {
	t.Helper()
	select {
	case w := <-l.output:
		p, e := dhcpv4.FromBytes(w.Payload)
		require.NoError(t, e)
		return p
	case <-time.After(3 * time.Second):
		t.Fatal("missing reply")
		return nil
	}
}

// Breaking the durable barrier (ACK on admission) makes this fail at the wire.
func TestRuntimeWireACKAfterCommit(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "dhcp")
	store, recovery, err := OpenLeaseStore(dir, 1024, nil)
	require.NoError(t, err)
	entered, release := make(chan struct{}), make(chan struct{})
	original := store.begin
	store.begin = func() (leaseTransaction, error) {
		tx, e := original()
		if e != nil {
			return nil, e
		}
		return &faultTransaction{leaseTransaction: tx, before: func() error { close(entered); <-release; return nil }}, nil
	}
	link := newRuntimeLink()
	rt, err := StartRuntime(context.Background(), fixtureSettings(), 1, link, quietProbe, store, recovery)
	require.NoError(t, err)
	defer rt.Close(context.Background())
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	initialInspection := rt.Inspect()
	link.input <- discover()
	offer := wireReceive(t, link)
	require.Equal(t, dhcpv4.MessageTypeOffer, offer.MessageType())
	req, err := dhcpv4.NewRequestFromOffer(offer)
	require.NoError(t, err)
	link.input <- req.ToBytes()
	<-entered
	applyDone := make(chan error, 1)
	updated := fixtureSettings()
	updated.LeaseSeconds = 120
	go func() { applyDone <- rt.Apply(context.Background(), updated, 2) }()
	select {
	case err := <-applyDone:
		t.Fatalf("configuration switch must drain commit, got %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	select {
	case p := <-link.output:
		t.Fatalf("ACK before commit: %x", p.Payload)
	case <-time.After(30 * time.Millisecond):
	}
	unblock()
	ack := wireReceive(t, link)
	assert.Equal(t, dhcpv4.MessageTypeAck, ack.MessageType())
	require.NoError(t, <-applyDone)
	assert.EqualValues(t, 2, rt.Projection().Generation)
	inspection := rt.Inspect()
	assert.Greater(t, inspection.Revision, initialInspection.Revision)
	require.Len(t, inspection.Leases, 1)
	inspection.Leases[0].Hostname = "mutated"
	assert.NotEqual(t, "mutated", rt.Inspect().Leases[0].Hostname)
	require.Eventually(t, func() bool { return len(rt.Projection().Leases) == 1 }, time.Second, time.Millisecond)
	view := rt.Projection()
	immutable := rt.View()
	require.NotNil(t, immutable)
	assert.EqualValues(t, 2, immutable.Generation())
	assert.Equal(t, 1, immutable.Len())
	settingsCopy := immutable.Settings()
	settingsCopy.LocalDomain = "mutated.arpa"
	assert.Equal(t, "home.arpa", immutable.Settings().LocalDomain)
	view.Leases[0].Hostname = "mutated"
	assert.NotEqual(t, "mutated", rt.Projection().Leases[0].Hostname)
	require.NoError(t, rt.Close(context.Background()))
	reopened, rows, err := OpenLeaseStore(dir, 1024, nil)
	require.NoError(t, err)
	defer reopened.Close(context.Background())
	require.Len(t, rows.Leases, 1)
	assert.Equal(t, ack.YourIPAddr.String(), rows.Leases[0].Address.String())
}

func TestRuntimeLateProbeCannotReplyAfterClose(t *testing.T) {
	store, rows, err := OpenLeaseStore(filepath.Join(t.TempDir(), "dhcp"), 1024, nil)
	require.NoError(t, err)
	entered := make(chan struct{})
	probe := func(ctx context.Context, _ netip.Addr) (bool, error) { close(entered); <-ctx.Done(); return false, nil }
	link := newRuntimeLink()
	rt, err := StartRuntime(context.Background(), fixtureSettings(), 1, link, probe, store, rows)
	require.NoError(t, err)
	link.input <- discover()
	<-entered
	require.NoError(t, rt.Close(context.Background()))
	assert.Empty(t, link.output)
}

func TestRuntimeUncertainCommitStopsAndRecoversWithoutACK(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "dhcp")
	store, rows, err := OpenLeaseStore(dir, 1024, nil)
	require.NoError(t, err)
	begin := store.begin
	store.begin = func() (leaseTransaction, error) {
		tx, e := begin()
		return faultTransaction{leaseTransaction: tx, after: errors.New("lost commit confirmation")}, e
	}
	link := newRuntimeLink()
	var probeCalls atomic.Int64
	rt, err := StartRuntime(context.Background(), fixtureSettings(), 1, link, func(ctx context.Context, a netip.Addr) (bool, error) { probeCalls.Add(1); return quietProbe(ctx, a) }, store, rows)
	require.NoError(t, err)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("probe calls=%d status=%+v leases=%+v", probeCalls.Load(), rt.Status(), rt.Leases())
		}
		_ = rt.Close(context.Background())
	})
	link.input <- discover()
	offer := wireReceive(t, link)
	req, err := dhcpv4.NewRequestFromOffer(offer)
	require.NoError(t, err)
	link.input <- req.ToBytes()
	select {
	case <-rt.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("uncertain store did not stop runtime")
	}
	assert.Equal(t, "uncertain", rt.Status().Storage)
	assert.NotEmpty(t, rt.Status().Error)
	assert.Empty(t, link.output)
	recovered, rows, err := OpenLeaseStore(dir, 1024, nil)
	require.NoError(t, err)
	require.Len(t, rows.Leases, 1)
	next := newRuntimeLink()
	replacement, err := StartRuntime(context.Background(), fixtureSettings(), 2, next, quietProbe, recovered, rows)
	require.NoError(t, err)
	defer replacement.Close(context.Background())
	next.input <- req.ToBytes()
	ack := wireReceive(t, next)
	assert.Equal(t, dhcpv4.MessageTypeAck, ack.MessageType())
}

func TestRuntimeClosingWriterRemainsOwned(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "dhcp")
	store, rows, err := OpenLeaseStore(dir, 1024, nil)
	require.NoError(t, err)
	entered, release := make(chan struct{}), make(chan struct{})
	begin := store.begin
	store.begin = func() (leaseTransaction, error) {
		tx, e := begin()
		return faultTransaction{leaseTransaction: tx, before: func() error { close(entered); <-release; return nil }}, e
	}
	link := newRuntimeLink()
	rt, err := StartRuntime(context.Background(), fixtureSettings(), 1, link, quietProbe, store, rows)
	require.NoError(t, err)
	link.input <- discover()
	offer := wireReceive(t, link)
	req, err := dhcpv4.NewRequestFromOffer(offer)
	require.NoError(t, err)
	link.input <- req.ToBytes()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, rt.Close(ctx), context.DeadlineExceeded)
	other, _, err := OpenLeaseStore(dir, 1024, nil)
	require.Error(t, err)
	require.Nil(t, other)
	close(release)
	require.NoError(t, rt.Close(context.Background()))
	assert.Empty(t, link.output)
	other, rows, err = OpenLeaseStore(dir, 1024, nil)
	require.NoError(t, err)
	defer other.Close(context.Background())
	require.Len(t, rows.Leases, 1)
}
