package policy

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSuffixIndexBuildReservation(t *testing.T) {
	for _, tc := range []struct {
		name string
		keys []string
	}{
		{"empty", nil},
		{"unique", []string{"\x04test\x01z", "\x04test", "\x04test\x01a"}},
		{"duplicates", []string{"\x04test\x01z", "\x04test", "\x04test\x01z", "\x04test", "\x04test\x01a"}},
		{"duplicate-heavy", strings.Split(strings.Repeat("\x04test,", 4095)+"\x04test", ",")},
		{"binary", []string{"\x04test\x03a.b", "\x04test\x01b\x01a", "\x04test\x03a\x00b", "\x04test\x03a.b", "\x04test\x02\xff\x00"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var in suffixBuild
			rules := make([]ruleMeta, len(tc.keys))
			want := make(map[string][]uint32)
			keyBytes := 0
			for i, key := range tc.keys {
				head := uint32(i + 1)
				in.add([]byte(key), head)
				if _, ok := want[key]; !ok {
					keyBytes += 1 + len(key)
				}
				want[key] = append(want[key], head)
			}
			var x suffixIndex
			x.build(in, rules)
			assert.Len(t, x.entries, len(want))
			assert.Equal(t, len(want), cap(x.entries), "reserve only unique entries")
			assert.Len(t, x.keys, keyBytes)
			assert.Equal(t, keyBytes, cap(x.keys), "reserve only unique key bytes")
			for key, heads := range want {
				var got []uint32
				for head := x.find(key); head != 0; head = rules[head-1].next {
					require.LessOrEqual(t, int(head), len(rules))
					require.Less(t, len(got), len(heads), "duplicate chain must terminate")
					got = append(got, head)
				}
				assert.ElementsMatch(t, heads, got, "every duplicate rule remains reachable")
			}
			assert.Zero(t, x.find("\x07missing\x04test"))
		})
	}
}

func TestReverseNameBinaryLabels(t *testing.T) {
	var buf [255]byte
	assert.Equal(t, "\x04test\x03a.b\x03x\x00y", string(reverseName(Name{wire: "\x03x\x00y\x03a.b\x04test"}, &buf)))
	assert.Equal(t, "\x04test\x01b\x01a", string(reverseName(Name{wire: "\x01a\x01b\x04test"}, &buf)))
}

func TestSuffixBuildArenaOwnershipAndGrowth(t *testing.T) {
	in := suffixBuild{keys: make([]byte, 0, 1)}
	key := []byte("\x04test\x03a.b")
	in.add(key, 1)
	clear(key) // Staging must copy the caller's reusable reverse-name buffer.
	for head := uint32(2); head <= 100; head++ {
		in.add([]byte("\x04test\x03x\x00y"), head)
	}
	var x suffixIndex
	rules := make([]ruleMeta, 100)
	x.build(in, rules)
	clear(in.keys)
	clear(in.entries)
	assert.Equal(t, uint32(1), x.find("\x04test\x03a.b"))
	head := x.find("\x04test\x03x\x00y")
	var got []uint32
	for head != 0 {
		require.LessOrEqual(t, head, uint32(len(rules)))
		require.Less(t, len(got), 99, "chain must terminate after arena growth")
		got = append(got, head)
		head = rules[head-1].next
	}
	want := make([]uint32, 99)
	for i := range want {
		want[i] = uint32(i + 2)
	}
	assert.ElementsMatch(t, want, got)
}
