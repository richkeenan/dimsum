package resolve

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/richkeenan/dimsum/internal/dnscache"
	"github.com/richkeenan/dimsum/internal/dnswire"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFlightCancellationAndBounds(t *testing.T) {
	var g flights
	defer g.close()
	var calls atomic.Int32
	release := make(chan struct{})
	work := func(ctx context.Context) ([]byte, error) {
		calls.Add(1)
		select {
		case <-release:
			return []byte{42}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	var key dnscache.Key
	f, err := g.join(key, false, work)
	require.NoError(t, err)
	for range maxWaiters - 1 {
		other, err := g.join(key, false, work)
		require.NoError(t, err)
		assert.Same(t, f, other)
	}
	_, err = g.join(key, false, work)
	assert.ErrorIs(t, err, errFlightFull)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = g.wait(ctx, key, f)
	assert.ErrorIs(t, err, context.Canceled)
	close(release)
	for range maxWaiters - 1 {
		b, err := g.wait(context.Background(), key, f)
		require.NoError(t, err)
		assert.Equal(t, []byte{42}, b)
	}
	assert.EqualValues(t, 1, calls.Load())
}

func TestFlightGlobalAndRefreshCapacity(t *testing.T) {
	for _, refresh := range []bool{false, true} {
		g := new(flights)
		work := func(ctx context.Context) ([]byte, error) { <-ctx.Done(); return nil, ctx.Err() }
		var q dnswire.Message
		require.NoError(t, dnswire.ParseRequest([]byte{0, 1, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 1, 'a', 0, 0, 1, 0, 1}, &q))
		limit := maxFlights
		if refresh {
			limit = maxRefresh
		}
		for i := range limit {
			k, ok := dnscache.NewKey(&q, uint64(i), 0, 0)
			require.True(t, ok)
			_, err := g.join(k, refresh, work)
			require.NoError(t, err)
		}
		k, ok := dnscache.NewKey(&q, uint64(limit), 0, 0)
		require.True(t, ok)
		_, err := g.join(k, refresh, work)
		assert.ErrorIs(t, err, errFlightFull)
		g.close()
		assert.Empty(t, g.active)
		assert.Zero(t, g.refreshes)
	}
}

func TestFlightLastWaiterAndClose(t *testing.T) {
	var g flights
	stopped := make(chan struct{})
	var key dnscache.Key
	f, err := g.join(key, false, func(ctx context.Context) ([]byte, error) { <-ctx.Done(); close(stopped); return nil, ctx.Err() })
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = g.wait(ctx, key, f)
	assert.ErrorIs(t, err, context.Canceled)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("last waiter did not cancel work")
	}
	g.close()
	_, err = g.join(key, false, nil)
	assert.Error(t, err)
}

func TestAbandonedFlightCannotCaptureNewClient(t *testing.T) {
	var g flights
	defer g.close()
	cleanup, canceled := make(chan struct{}), make(chan struct{})
	defer close(cleanup)
	var key dnscache.Key
	old, err := g.join(key, false, func(ctx context.Context) ([]byte, error) {
		<-ctx.Done()
		close(canceled)
		<-cleanup
		return nil, ctx.Err()
	})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = g.wait(ctx, key, old)
	require.ErrorIs(t, err, context.Canceled)
	<-canceled
	fresh, err := g.join(key, false, func(ctx context.Context) ([]byte, error) { return []byte{99}, ctx.Err() })
	require.NoError(t, err)
	assert.NotSame(t, old, fresh)
	b, err := g.wait(context.Background(), key, fresh)
	require.NoError(t, err)
	assert.Equal(t, []byte{99}, b)
	g.mu.Lock()
	assert.Equal(t, 1, g.workers, "abandoned worker remains charged through cleanup")
	g.mu.Unlock()
}

func TestSharedContextUsesUpstreamDeadline(t *testing.T) {
	var g flights
	defer g.close()
	var key dnscache.Key
	for _, refresh := range []bool{false, true} {
		f, err := g.join(key, refresh, func(ctx context.Context) ([]byte, error) {
			// ExchangeRoute sets its own configured timeout, up to 60 seconds.
			// No shorter coalescer deadline may override it.
			_, limited := ctx.Deadline()
			if limited {
				return []byte{1}, nil
			}
			return []byte{0}, nil
		})
		require.NoError(t, err)
		<-f.done
		assert.Equal(t, []byte{0}, f.wire)
	}
}
