package dhcp

import (
	"context"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"
)

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
	case <-time.After(2 * time.Second):
		t.Fatal("500ms probe deadline not enforced")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("scheduler did not stop")
	}
	assert.Zero(t, active.Load())
}
