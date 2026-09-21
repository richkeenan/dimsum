package upstream

import (
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdaptivePriorityExplorationAndProbeLease(t *testing.T) {
	a := netip.MustParseAddrPort("127.0.0.1:1053")
	c, err := New(Options{Endpoints: []netip.AddrPort{a, a}, Fallback: []netip.AddrPort{a}, Mode: "adaptive"})
	require.NoError(t, err)
	c.health[0].Latency = 20 * time.Millisecond
	c.health[1].Latency = time.Millisecond
	assert.Equal(t, []int{1, 0, 2}, c.order())
	c.selections = 15
	assert.Equal(t, []int{0, 1, 2}, c.order())
	c.options.Mode = "ordered"
	assert.Equal(t, []int{0, 1, 2}, c.order())
	_, old := c.claim(0)
	c.health[0].RetryAt = time.Now().Add(-time.Second)
	ok, lease := c.claim(0)
	require.True(t, ok)
	ok, _ = c.claim(0)
	assert.False(t, ok, "only one half-open probe")
	c.record(0, old, time.Millisecond, nil, 0, false)
	assert.Equal(t, "half-open", c.Health()[0].State, "late completion cannot release someone else's probe")
	c.record(0, lease, time.Millisecond, nil, 0, false)
	assert.Equal(t, "closed", c.Health()[0].State)
}
