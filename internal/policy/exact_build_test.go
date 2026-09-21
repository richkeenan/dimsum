package policy

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExactBuildPagesAndOwnership(t *testing.T) {
	var a exactBuildArena
	key := strings.Repeat("x", 254)
	var offsets []uint32
	for range 257 {
		off, err := a.add(key, 1<<20)
		require.NoError(t, err)
		offsets = append(offsets, off)
	}
	off, err := a.add("\x00\xff", 1<<20)
	require.NoError(t, err)
	assert.EqualValues(t, exactBuildPageBytes, off)
	assert.Equal(t, "\x00\xff", a.key(off))
	x := exactIndex{keys: a.finish()}
	assert.EqualValues(t, exactBuildPageBytes+3, cap(x.keys))
	for _, off := range offsets {
		assert.Equal(t, key, x.key(off))
	}
	for _, page := range a.pages {
		clear(page)
	}
	assert.Equal(t, "\x00\xff", x.key(off))
	assert.Equal(t, key, x.key(offsets[0]))
}

func TestExactBuildCollisionAndDuplicate(t *testing.T) {
	var a exactBuildArena
	x := exactIndex{slots: make([]exactSlot, 8)}
	for i, key := range []string{"one", "two", "three"} {
		old, err := x.insertStaged(key, 1, uint32(i+1), &a, 1024)
		require.NoError(t, err)
		assert.Zero(t, old)
	}
	before := a.size()
	old, err := x.insertStaged("two", 1, 4, &a, 1024)
	require.NoError(t, err)
	assert.EqualValues(t, 2, old)
	assert.Equal(t, before, a.size())
	x.keys = a.finish()
	assert.EqualValues(t, 1, x.find("one", 1))
	assert.EqualValues(t, 4, x.find("two", 1))
	assert.EqualValues(t, 3, x.find("three", 1))
	assert.Zero(t, x.find("absent", 1))
	assert.EqualValues(t, 14, cap(x.keys))
}

func TestExactBuildBounds(t *testing.T) {
	var a exactBuildArena
	_, err := a.add(strings.Repeat("x", 255), 1024)
	require.Error(t, err)
	assert.Empty(t, a.pages)
	_, err = a.add("abc", 3)
	require.Error(t, err)
	assert.Empty(t, a.pages)
	_, err = a.add("abc", 4)
	require.NoError(t, err)
	_, err = a.add("d", 5)
	assert.Error(t, err)
	assert.EqualValues(t, 4, a.size())
}
