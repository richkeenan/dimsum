package policy

import "sort"

type suffixEntry struct{ off, head uint32 }
type suffixIndex struct {
	entries []suffixEntry
	keys    []byte
}
type suffixBuild struct {
	key  string
	head uint32
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
	return string(x.keys[uint64(off)+1 : uint64(off)+1+uint64(x.keys[off])])
}
func (x *suffixIndex) build(in []suffixBuild, rules []ruleMeta) {
	sort.Slice(in, func(i, j int) bool { return in[i].key < in[j].key })
	for _, v := range in {
		if len(x.entries) > 0 && x.key(x.entries[len(x.entries)-1].off) == v.key {
			p := &x.entries[len(x.entries)-1]
			rules[v.head-1].next = p.head
			p.head = v.head
		} else {
			x.entries = append(x.entries, suffixEntry{uint32(len(x.keys)), v.head})
			x.keys = append(x.keys, byte(len(v.key)))
			x.keys = append(x.keys, v.key...)
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
