package transport

import (
	"context"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"runtime"
	"testing"
)

func TestTransportSlots(t *testing.T) {
	p := newSlots(2, 1)
	a := p.acquire(12)
	b := p.acquire(2048)
	c := p.acquire(2049)
	require.NotNil(t, a)
	require.NotNil(t, b)
	require.NotNil(t, c)
	assert.Nil(t, p.acquire(12))
	assert.Nil(t, p.acquire(65535))
	assert.Nil(t, p.acquire(65536))
	for i := range a.wire {
		a.wire[i] = 0xa5
	}
	p.release(a)
	d := p.acquire(12)
	require.NotNil(t, d)
	assert.Same(t, a, d)
	p.release(b)
	p.release(c)
	p.release(d)
	assert.Equal(t, 2*2048+65535, p.bytes())
}

func TestTransportPoolPoisonAndGC(t *testing.T) {
	p := newSlots(1, 1)
	for range 50 {
		slot := p.acquire(20)
		require.NotNil(t, slot)
		for i := range slot.wire {
			slot.wire[i] = 0xa5
		}
		copy(slot.wire, []byte("new owner"))
		p.release(slot)
		runtime.GC()
		slot = p.acquire(20)
		require.NotNil(t, slot)
		assert.Equal(t, "new owner", string(slot.wire[:9]))
		assert.Nil(t, p.acquire(20))
		p.release(slot)
	}
}

func TestTransportConfiguration(t *testing.T) {
	_, err := New(Options{}, nil)
	assert.Error(t, err)
	h := HandlerFunc(func(context.Context, *Request, []byte) (int, error) { return 0, nil })
	for _, o := range []Options{{SmallSlots: -1}, {UDPSize: 511}, {UDPSize: 65536}, {ReadTimeout: -1}} {
		_, err = New(o, h)
		assert.Error(t, err)
	}
	s, err := New(Options{}, h)
	require.NoError(t, err)
	assert.Equal(t, 4096*2048+16*65535, s.StorageBytes())
}
