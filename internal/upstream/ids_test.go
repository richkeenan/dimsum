package upstream

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExchangeIDQuarantineAndReuse(t *testing.T) {
	var p ids
	for i := range p.active {
		p.active[i] = true
	}
	p.active[123] = false
	id, err := p.acquire()
	require.NoError(t, err)
	assert.EqualValues(t, 123, id)
	_, err = p.acquire()
	assert.ErrorIs(t, err, ErrOverloaded, "live ID reused")
	p.release(id, time.Second)
	_, err = p.acquire()
	assert.ErrorIs(t, err, ErrOverloaded, "recently completed ID reused")
	p.until[id] = time.Now().Add(-time.Second)
	reused, err := p.acquire()
	require.NoError(t, err)
	assert.Equal(t, id, reused)
}
