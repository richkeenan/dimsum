package policy

import (
	"bytes"
	"sort"
)

type suffixEntry struct{ off, head uint32 }
type suffixIndex struct {
	entries []suffixEntry
	keys    []byte
}
type suffixBuild struct {
	entries []suffixEntry
	keys    []byte
}

func (b *suffixBuild) add(key []byte, head uint32) {
	b.entries = append(b.entries, suffixEntry{uint32(len(b.keys)), head})
	b.keys = append(b.keys, byte(len(key)))
	b.keys = append(b.keys, key...)
}

func suffixKey(keys []byte, off uint32) []byte {
	return keys[uint64(off)+1 : uint64(off)+1+uint64(keys[off])]
}

// reverseName reverses labels, not bytes within a label. Length bytes preserve
// binary dots/NULs and make every prefix boundary unambiguous. Caller owns buf.
func reverseName(n Name, buf *[255]byte) []byte {
	var offsets [127]uint8
	count := 0
	for i := 0; i < len(n.wire); i += 1 + int(n.wire[i]) {
		offsets[count] = uint8(i)
		count++
	}
	end := 0
	for i := count - 1; i >= 0; i-- {
		start := int(offsets[i])
		size := 1 + int(n.wire[start])
		end += copy(buf[end:], n.wire[start:start+size])
	}
	return buf[:end]
}
func (x *suffixIndex) key(off uint32) string {
	return string(suffixKey(x.keys, off))
}
func (x *suffixIndex) build(in suffixBuild, rules []ruleMeta) {
	sort.Slice(in.entries, func(i, j int) bool {
		return bytes.Compare(suffixKey(in.keys, in.entries[i].off), suffixKey(in.keys, in.entries[j].off)) < 0
	})
	// Count after sorting so duplicate memberships reserve no extra index space.
	// Exact arenas avoid geometric growth and its transient copies during refresh.
	entries, keyBytes := 0, 0
	for i, v := range in.entries {
		key := suffixKey(in.keys, v.off)
		if i == 0 || !bytes.Equal(key, suffixKey(in.keys, in.entries[i-1].off)) {
			entries++
			keyBytes += 1 + len(key)
		}
	}
	x.entries = make([]suffixEntry, 0, entries)
	x.keys = make([]byte, 0, keyBytes)
	for _, v := range in.entries {
		key := suffixKey(in.keys, v.off)
		if len(x.entries) > 0 && bytes.Equal(suffixKey(x.keys, x.entries[len(x.entries)-1].off), key) {
			p := &x.entries[len(x.entries)-1]
			rules[v.head-1].next = p.head
			p.head = v.head
		} else {
			x.entries = append(x.entries, suffixEntry{uint32(len(x.keys)), v.head})
			x.keys = append(x.keys, byte(len(key)))
			x.keys = append(x.keys, key...)
		}
	}
}
func (x *suffixIndex) find(key string) uint32 {
	i := sort.Search(len(x.entries), func(i int) bool { return x.key(x.entries[i].off) >= key })
	if i < len(x.entries) && x.key(x.entries[i].off) == key {
		return x.entries[i].head
	}
	return 0
}
