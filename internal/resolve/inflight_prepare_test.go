package resolve

import (
	"context"
	"testing"

	"github.com/richkeenan/dimsum/internal/dnscache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFlightPreparesOwnedInputOnlyForAdmittedLeader(t *testing.T) {
	var g flights
	defer g.close()
	input := []byte("original")
	var owned []byte
	prepared := 0
	prepare := func() {
		prepared++
		owned = append([]byte(nil), input...)
	}
	release := make(chan struct{})
	work := func(ctx context.Context) ([]byte, error) {
		select {
		case <-release:
			return owned, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	k := dnscache.Key{}
	leader, joined, err := g.joinStatus(k, false, work, nil, prepare)
	require.NoError(t, err)
	assert.False(t, joined)
	assert.Equal(t, 1, prepared, "ownership must be established before returning")
	copy(input, "modified")
	follower, joined, err := g.joinStatus(k, false, work, nil, prepare)
	require.NoError(t, err)
	assert.True(t, joined)
	assert.Equal(t, 1, prepared, "followers must not copy input")
	close(release)
	for _, f := range []*flight{leader, follower} {
		response, err := g.wait(context.Background(), k, f)
		require.NoError(t, err)
		assert.Equal(t, "original", string(response))
	}
	g.close()
	_, _, err = g.joinStatus(k, false, work, nil, prepare)
	require.Error(t, err)
	assert.Equal(t, 1, prepared, "rejected requests must not prepare work")
}
