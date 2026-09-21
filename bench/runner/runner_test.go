package runner_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/richkeenan/dimsum/bench/runner"
	"github.com/richkeenan/dimsum/bench/workload"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func trace(t *testing.T, count int) workload.Workload {
	t.Helper()
	w, err := workload.New(workload.Spec{Family: "churn", Seed: 42, Count: count, Rate: 1_000_000_000})
	require.NoError(t, err)
	return w
}

func TestScheduledLatencyIncludesQueueAndService(t *testing.T) {
	r, err := runner.Run(context.Background(), trace(t, 4), runner.Options{Workers: 1, Queue: 4, Timeout: time.Second}, func() runner.Handler {
		return func(context.Context, workload.Query) error { time.Sleep(5 * time.Millisecond); return nil }
	})
	require.NoError(t, err)
	assert.Equal(t, 4, r.Offered)
	assert.Equal(t, 4, r.Counts[runner.Answered])
	for i, s := range r.Samples {
		assert.Equal(t, time.Duration(i), s.Scheduled)
		assert.Equal(t, s.Finished-s.Scheduled, s.Latency)
		assert.Equal(t, s.Finished-s.Started, s.Service)
		if i > 0 {
			assert.GreaterOrEqual(t, s.Started, r.Samples[i-1].Finished)
		}
	}
	assert.Greater(t, r.Samples[3].Latency, r.Samples[3].Service)
}

func TestOverflowDoesNotReduceOfferedLoad(t *testing.T) {
	r, err := runner.Run(context.Background(), trace(t, 1000), runner.Options{Workers: 1, Queue: 1, Timeout: 50 * time.Millisecond}, func() runner.Handler {
		return func(ctx context.Context, _ workload.Query) error { <-ctx.Done(); return ctx.Err() }
	})
	require.NoError(t, err)
	assert.Equal(t, 1000, r.Offered)
	assert.GreaterOrEqual(t, r.Counts[runner.Overflow], 998)
	assert.Equal(t, 1000, r.Counts[runner.Overflow]+r.Counts[runner.Timeout])
	assert.Zero(t, r.Counts[runner.NotOffered])
}

func TestExpiredQueueDoesNotCallHandler(t *testing.T) {
	calls := 0 // one worker, read only after Run joins
	r, err := runner.Run(context.Background(), trace(t, 3), runner.Options{Workers: 1, Queue: 3, Timeout: 20 * time.Millisecond}, func() runner.Handler {
		return func(ctx context.Context, _ workload.Query) error {
			calls++
			<-ctx.Done()
			time.Sleep(time.Millisecond) // ensure every nanosecond-spaced arrival expired
			return ctx.Err()
		}
	})
	require.NoError(t, err)
	assert.LessOrEqual(t, calls, 1)
	assert.Equal(t, 3, r.Counts[runner.Timeout])
	assert.Zero(t, r.Samples[2].Service)
}

func TestCancellationAndFailureAccounting(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	factory := func() runner.Handler {
		return func(context.Context, workload.Query) error { return errors.New("fixture failure") }
	}
	r, err := runner.Run(ctx, trace(t, 3), runner.Options{Workers: 1, Queue: 3, Timeout: time.Second}, factory)
	require.ErrorIs(t, err, context.Canceled)
	assert.Zero(t, r.Offered)
	assert.Equal(t, 3, r.Counts[runner.NotOffered])
	r, err = runner.Run(context.Background(), trace(t, 3), runner.Options{Workers: 1, Queue: 3, Timeout: time.Second}, factory)
	require.NoError(t, err)
	assert.Equal(t, 3, r.Counts[runner.Failed])
}

func TestInvalidLimits(t *testing.T) {
	_, err := runner.Run(context.Background(), workload.Workload{}, runner.Options{}, nil)
	assert.Error(t, err)
	_, err = runner.Run(context.Background(), trace(t, 1), runner.Options{Workers: 257, Queue: 1, Timeout: time.Second}, func() runner.Handler { return nil })
	assert.Error(t, err)
	_, err = runner.Run(context.Background(), trace(t, 1), runner.Options{Workers: 1, Queue: 1, Timeout: time.Second}, func() runner.Handler { return nil })
	assert.Error(t, err)
}
