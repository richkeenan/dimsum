package dnscache

import "sync"

const ways = 8
const referenced = 1
const negative = 2

// slot is 40 bytes on arm64/amd64 and contains no GC pointers. Hash buckets
// contain eight slots; full keys are verified even on equal 64-bit hashes.
// Generation tags are unnecessary here: all reads, reuse and copies happen
// under one shard mutex and neither handles nor borrowed blobs escape it.
type slot struct {
	hash                               uint64
	inserted                           int64
	offset, allocated, lifetime        uint32
	keyLen, messageLen, patches, flags uint16
}

type shard struct {
	mu                           sync.Mutex
	slots                        []slot
	arena                        []byte
	used                         []uint64
	hand, cursor                 int
	entries, occupied, evictions int
}

func (s *shard) bucket(hash uint64) int { return int(hash%uint64(len(s.slots)/ways)) * ways }
