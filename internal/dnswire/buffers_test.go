package dnswire_test

import (
	"testing"

	"github.com/richkeenan/dimsum/internal/dnswire"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMessageBuffersKeepConcurrentBorrowsIndependent(t *testing.T) {
	p := dnswire.NewMessageBuffers(1)
	a, b := p.Get(), p.Get()
	a[0], b[0] = 17, 29
	assert.EqualValues(t, 17, a[0])
	assert.EqualValues(t, 29, b[0])
	p.Put(a)
	c := p.Get()
	require.Same(t, a, c)
	c[0] = 42
	assert.EqualValues(t, 29, b[0], "a returned scratch buffer cannot alias an outstanding borrower")
	p.Put(c)
	p.Put(b)
	first, second := p.Get(), p.Get()
	assert.Same(t, a, first)
	assert.NotSame(t, b, second, "only one returned message may be retained")
	assert.Zero(t, second[0])
}
