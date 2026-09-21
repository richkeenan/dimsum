package policy

import "hash/maphash"

// Sixteen bytes per slot. Key length is stored as a byte in the shared arena;
// DNS canonical keys contain at most 254 bytes. head is a full uint32 rule ID.
type exactSlot struct {
	hash      uint64
	off, head uint32
}
type exactIndex struct {
	seed  maphash.Seed
	slots []exactSlot
	keys  []byte
}

func (x *exactIndex) key(off uint32) string {
	return string(x.keys[uint64(off)+1 : uint64(off)+1+uint64(x.keys[off])])
}

func (x *exactIndex) insert(key string, hash uint64, head uint32) uint32 {
	for i := hash % uint64(len(x.slots)); ; i = (i + 1) % uint64(len(x.slots)) {
		p := &x.slots[i]
		if p.head == 0 {
			p.hash = hash
			p.off = uint32(len(x.keys))
			p.head = head
			x.keys = append(x.keys, byte(len(key)))
			x.keys = append(x.keys, key...)
			return 0
		}
		if p.hash == hash && x.key(p.off) == key {
			old := p.head
			p.head = head
			return old
		}
	}
}

func (x *exactIndex) find(key string, hash uint64) uint32 {
	if len(x.slots) == 0 {
		return 0
	}
	for i := hash % uint64(len(x.slots)); ; i = (i + 1) % uint64(len(x.slots)) {
		p := x.slots[i]
		if p.head == 0 {
			return 0
		}
		// Hashes only select candidates. Full length-prefixed bytes decide membership.
		if p.hash == hash && x.key(p.off) == key {
			return p.head
		}
	}
}
