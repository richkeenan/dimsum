package control

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCloseCancelsAndJoinsJob(t *testing.T) {
	entered := make(chan struct{})
	s := New(Options{Jobs: map[string]func(context.Context, json.RawMessage) (any, error){"test": func(ctx context.Context, _ json.RawMessage) (any, error) {
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	}}})
	_, err := s.StartJob(context.Background(), "test", nil)
	require.NoError(t, err)
	<-entered
	done := make(chan struct{})
	go func() { s.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("close did not join cancelled job")
	}
	_, err = s.StartJob(context.Background(), "test", nil)
	assert.ErrorIs(t, err, ErrUnavailable)
	require.Len(t, s.Jobs(), 1)
	assert.Equal(t, "failed", s.Jobs()[0].State)
}
