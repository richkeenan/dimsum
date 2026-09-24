package resolve

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/richkeenan/dimsum/internal/dnswire"
	"github.com/richkeenan/dimsum/internal/testutil"
	"github.com/richkeenan/dimsum/internal/transport"
	"github.com/richkeenan/dimsum/internal/upstream"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSharedResponseSurvivesScratchBufferReuse(t *testing.T) {
	u, err := testutil.NewUpstream(testutil.NewClock(time.Now()), func(req testutil.Request) testutil.Response {
		wire := append([]byte(nil), req.Wire...)
		wire[2] |= 0x80
		return testutil.Response{Wire: wire}
	})
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, u.Close()) })
	c, err := upstream.New(upstream.Options{Endpoints: []upstream.Endpoint{upstream.PlainEndpoint(netip.MustParseAddrPort(u.Address()))}})
	require.NoError(t, err)
	p := New(c)
	t.Cleanup(func() { assert.NoError(t, p.Close()) })
	cache, _, err := p.cacheFor(nil)
	require.NoError(t, err)
	var first, original []byte
	for _, label := range []byte{'a', 'b', 'c'} {
		r := transport.Request{Wire: []byte{0, 1, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 1, label, 0, 0, 1, 0, 1}}
		require.NoError(t, dnswire.ParseRequest(r.Wire, &r.Message))
		key, ok := cacheKey(&r, nil)
		require.True(t, ok)
		response, err := p.shared(context.Background(), nil, upstream.DefaultRoute, cache, key, &r, false)
		require.NoError(t, err)
		if first == nil {
			first = response
			original = append([]byte(nil), response...)
		}
		assert.Equal(t, original, first, "a completed flight's waiters must retain immutable bytes after later exchanges")
	}
	// Poison every retained scratch buffer, including the one used for first.
	var buffers []*[dnswire.MaxMessageLen]byte
	for range 64 {
		b := p.cache.buffers.Get()
		for i := range b {
			b[i] = 0xa5
		}
		buffers = append(buffers, b)
	}
	assert.Equal(t, original, first)
	for _, b := range buffers {
		p.cache.buffers.Put(b)
	}
}
