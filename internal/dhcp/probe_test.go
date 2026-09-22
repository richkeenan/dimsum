package dhcp

import (
	"context"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"net/netip"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestProbeSchedulerCompletionAdmitsImmediateConflictReplacement(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e, err := NewEngine(fixtureSettings(), 1, time.Now)
		require.NoError(t, err)
		first := e.Handle(client(1, Discover)).Probe
		second := e.Handle(client(2, Discover)).Probe
		require.NotNil(t, first)
		require.NotNil(t, second)
		finish := make(chan struct{})
		s := NewProbeScheduler(func(ctx context.Context, ip netip.Addr) (bool, error) {
			if ip == second.Address {
				<-ctx.Done()
				return false, ctx.Err()
			}
			select {
			case <-finish:
				return ip == first.Address, nil
			case <-ctx.Done():
				return false, ctx.Err()
			}
		})
		// A rendezvous result channel holds the worker at the publication boundary,
		// making admission ordering observable without scheduler timing or hooks.
		s.results = make(chan ProbeResult)
		require.True(t, s.Submit(*first))
		require.True(t, s.Submit(*second))
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { s.Run(ctx); close(done) }()
		defer func() { cancel(); <-done }()
		synctest.Wait()
		assert.False(t, s.Submit(*first), "both probes are occupied")
		close(finish)
		synctest.Wait()
		assert.Len(t, s.idle, 1, "admission must be ready before completion is visible")
		result := <-s.Results()
		require.NoError(t, result.Err)
		require.True(t, result.Conflict)
		out := e.CompleteProbe(result)
		require.NotNil(t, out.Mutation)
		require.NotNil(t, out.Probe)
		require.True(t, s.Submit(*out.Probe), "conflict replacement must not be dropped as probe busy")
		result = <-s.Results()
		require.NoError(t, result.Err)
		out = e.CompleteProbe(result)
		require.NotNil(t, out.Reply)
		assert.Equal(t, Offer, out.Reply.Type)
	})
}

func TestProbeSchedulerBackpressureKeepsBoundedHandoffsAndStops(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := NewProbeScheduler(func(context.Context, netip.Addr) (bool, error) { return false, nil })
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { s.Run(ctx); close(done) }()
		defer func() { cancel(); <-done }()
		job := Probe{Address: netip.MustParseAddr("192.0.2.100"), Deadline: time.Now().Add(time.Second)}
		for range 2 {
			for range 2 {
				job.Token.Sequence++
				require.True(t, s.Submit(job))
			}
			synctest.Wait()
		}
		assert.Len(t, s.results, 2, "completed results remain bounded under backpressure")
		// Both workers are delivering results. Each permits just one next handoff.
		require.True(t, s.Submit(job))
		require.True(t, s.Submit(job))
		assert.False(t, s.Submit(job))
		cancel()
		synctest.Wait()
		assert.True(t, s.stopped.Load())
		assert.False(t, s.Submit(job))
	})
}

func TestProbeSchedulerAllowsRepeatedObservation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := NewProbeScheduler(func(ctx context.Context, _ netip.Addr) (bool, error) {
			select {
			case <-time.After(1500 * time.Millisecond):
				return false, nil
			case <-ctx.Done():
				return false, ctx.Err()
			}
		})
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		go s.Run(ctx)
		require.True(t, s.Submit(Probe{Deadline: time.Now().Add(2 * time.Second)}))
		result := <-s.Results()
		assert.NoError(t, result.Err, "scheduler must allow the full repeated-probe window")
	})
}

func TestProbeSchedulerBoundsDeadlineAndShutdown(t *testing.T) {
	var active, peak atomic.Int32
	started := make(chan struct{}, 2)
	s := NewProbeScheduler(func(ctx context.Context, ip netip.Addr) (bool, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
		}
		started <- struct{}{}
		<-ctx.Done()
		return false, ctx.Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	job := Probe{Token: Token{1, 1}, Address: netip.MustParseAddr("192.0.2.100"), Deadline: time.Now().Add(time.Minute)}
	require.Eventually(t, func() bool { return s.Submit(job) }, time.Second, time.Millisecond)
	<-started
	job.Token.Sequence++
	require.Eventually(t, func() bool { return s.Submit(job) }, time.Second, time.Millisecond)
	<-started
	assert.False(t, s.Submit(job))
	assert.EqualValues(t, 2, peak.Load())
	select {
	case result := <-s.Results():
		assert.Error(t, result.Err)
	case <-time.After(3 * time.Second):
		t.Fatal("bounded probe deadline not enforced")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("scheduler did not stop")
	}
	assert.Zero(t, active.Load())
}

func TestProbeSchedulerReservesOnlyTwoStartupHandoffs(t *testing.T) {
	s := NewProbeScheduler(func(context.Context, netip.Addr) (bool, error) { return false, nil })
	job := Probe{Token: Token{1, 1}, Address: netip.MustParseAddr("192.0.2.100"), Deadline: time.Now().Add(time.Second)}
	require.True(t, s.Submit(job), "first worker slot must not depend on goroutine scheduling")
	job.Token.Sequence = 2
	require.True(t, s.Submit(job))
	assert.False(t, s.Submit(job), "no waiting backlog beyond two worker handoffs")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	for range 2 {
		select {
		case result := <-s.Results():
			assert.NoError(t, result.Err)
		case <-time.After(time.Second):
			t.Fatal("reserved handoff lost")
		}
	}
	cancel()
	<-done
	assert.False(t, s.Submit(job))
}
