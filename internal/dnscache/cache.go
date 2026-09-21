package dnscache

import (
	"bytes"
	"encoding/binary"
	"errors"
	"github.com/richkeenan/dimsum/internal/dnswire"
	"hash/maphash"
	"time"
	"unsafe"
)

const MaxMessage = 16 << 10
const page = 8192

type Config struct {
	Bytes, Shards int
	// Zero selects 300 seconds. Positive answers retain their original TTLs.
	MaxNegativeTTL uint32
}

type Cache struct {
	shards      []shard
	seed        maphash.Seed
	epoch       time.Time
	retained    int
	maxNegative uint32
}

// CacheResult describes a fresh full-size reply copied into caller storage.
// On a miss dst is unchanged. No arena slice or reference escapes a lookup.
type CacheResult struct {
	Hit, Negative bool
	Length        int
}
type ShardStats struct{ Entries, OccupiedBytes, ArenaBytes, Slots, Evictions int }
type Stats struct {
	RetainedBytes int
	ShardCount    int
	Shards        [16]ShardStats
}

func rounded(n int) int { return (n + page - 1) / page * page }

// New preallocates all retained storage. Budget accounting charges each backing
// allocation rounded UP to an 8-KiB Go heap page, including the Cache and shard
// array, slots and allocation bitmap. This conservatively covers small-object
// size classes and large-object page rounding on supported Go/arm64/amd64.
// Runtime-wide allocator bookkeeping and goroutine stacks are not cache-owned.
func New(cfg Config) (*Cache, error) {
	if cfg.Shards == 0 {
		cfg.Shards = 4
	}
	if cfg.Shards < 1 || cfg.Shards > 16 || cfg.Bytes < 0 || cfg.Bytes > 1<<30 {
		return nil, errors.New("dnscache: invalid budget or shard count")
	}
	base := rounded(int(unsafe.Sizeof(Cache{}))) + rounded(cfg.Shards*int(unsafe.Sizeof(shard{})))
	per := (cfg.Bytes - base) / cfg.Shards
	blocks, slots, cost := per/blockSize, 0, 0
	for blocks >= 32 {
		slots = max(ways, blocks/2/ways*ways)
		cost = rounded(blocks*blockSize) + rounded(slots*int(unsafe.Sizeof(slot{}))) + rounded((blocks+63)/64*8)
		if cost <= per {
			break
		}
		blocks--
	}
	if blocks < 32 {
		return nil, errors.New("dnscache: budget too small")
	}
	if cfg.MaxNegativeTTL == 0 {
		cfg.MaxNegativeTTL = 300
	}
	c := &Cache{shards: make([]shard, cfg.Shards), seed: maphash.MakeSeed(), epoch: time.Now(), retained: base + cost*cfg.Shards, maxNegative: cfg.MaxNegativeTTL}
	for i := range c.shards {
		s := &c.shards[i]
		s.slots = make([]slot, slots)
		s.arena = make([]byte, blocks*blockSize)
		s.used = make([]uint64, (blocks+63)/64)
	}
	return c, nil
}

func (c *Cache) hash(k *Key) uint64 { return maphash.Bytes(c.seed, k.data[:k.length]) }
func (c *Cache) Put(k Key, response []byte, now time.Time) bool {
	if !k.valid() {
		return false
	}
	return c.put(k, response, now, c.hash(&k))
}

// Put accepts upstream originals only, after endpoint/ID/namespace validation.
// It independently checks wire validity, key equality and template eligibility.
// It never retains response storage. False means bypass, not a resolver error.
func (c *Cache) put(k Key, response []byte, now time.Time, hash uint64) bool {
	if !k.valid() || len(response) > MaxMessage {
		return false
	}
	var m dnswire.Message
	if dnswire.ScanMessage(response, &m) != nil || !k.matches(&m) {
		return false
	}
	var table [MaxMessage / 11 * 8]byte
	info, err := dnswire.PrepareTemplate(response, table[:], c.maxNegative)
	if err != nil {
		return false
	}
	s := &c.shards[hash%uint64(len(c.shards))]
	// Divide out shard bits so every shard can use every bucket.
	bucket := s.bucket(hash / uint64(len(c.shards)))
	size := int(k.length) + len(response) + info.Patches*8
	if size > len(s.arena) {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	target := -1
	for i := bucket; i < bucket+ways; i++ {
		x := &s.slots[i]
		if x.allocated == 0 {
			target = i
			continue
		}
		if x.hash == hash && x.keyLen == k.length && bytes.Equal(s.arena[x.offset:int(x.offset)+int(x.keyLen)], k.data[:k.length]) {
			s.remove(i)
			target = i
			break
		}
	}
	if target < 0 {
		// Bucket-local second chance, with rotating start shared with arena CLOCK.
		for n := 0; n < 2*ways; n++ {
			i := bucket + s.hand%ways
			s.hand = (s.hand + 1) % len(s.slots)
			if s.slots[i].flags&referenced != 0 {
				s.slots[i].flags &^= referenced
				continue
			}
			s.remove(i)
			s.evictions++
			target = i
			break
		}
	}
	for {
		off, allocated, ok := s.allocate(size)
		if ok {
			blob := s.arena[off : off+size]
			copy(blob, k.data[:k.length])
			copy(blob[k.length:], response)
			copy(blob[int(k.length)+len(response):], table[:info.Patches*8])
			flags := uint16(referenced)
			if info.Negative {
				flags |= negative
			}
			s.slots[target] = slot{hash: hash, inserted: c.tick(now), offset: uint32(off), allocated: uint32(allocated), lifetime: info.Lifetime, keyLen: k.length, messageLen: uint16(len(response)), patches: uint16(info.Patches), flags: flags}
			s.entries++
			return true
		}
		if !s.evict() {
			return false
		}
	}
}

func (c *Cache) Get(k Key, request *dnswire.Message, dst []byte, now time.Time) CacheResult {
	if !k.valid() {
		return CacheResult{}
	}
	return c.get(k, request, dst, now, c.hash(&k))
}

func (c *Cache) get(k Key, request *dnswire.Message, dst []byte, now time.Time, hash uint64) CacheResult {
	if !k.valid() {
		return CacheResult{}
	}
	want, ok := NewKey(request, binary.BigEndian.Uint64(k.data[:]), binary.BigEndian.Uint64(k.data[8:]), binary.BigEndian.Uint64(k.data[16:]))
	if !ok || want != k {
		return CacheResult{}
	}
	s := &c.shards[hash%uint64(len(c.shards))]
	bucket := s.bucket(hash / uint64(len(c.shards)))
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := bucket; i < bucket+ways; i++ {
		x := &s.slots[i]
		if x.allocated == 0 || x.hash != hash || x.keyLen != k.length {
			continue
		}
		blob := s.arena[x.offset : int(x.offset)+int(x.allocated)]
		if !bytes.Equal(blob[:x.keyLen], k.data[:k.length]) {
			continue
		}
		elapsed, valid := age(c.tick(now), x.inserted)
		if !valid {
			return CacheResult{}
		}
		if elapsed >= uint64(x.lifetime) {
			s.remove(i)
			return CacheResult{}
		}
		if len(dst) < int(x.messageLen) {
			return CacheResult{}
		}
		message := blob[x.keyLen : int(x.keyLen)+int(x.messageLen)]
		copy(dst, message)
		binary.BigEndian.PutUint16(dst, request.Question.Header.ID)
		flags := binary.BigEndian.Uint16(dst[2:]) &^ (dnswire.FlagAD | dnswire.FlagRD | dnswire.FlagCD)
		flags |= request.Question.Header.Flags & (dnswire.FlagRD | dnswire.FlagCD)
		binary.BigEndian.PutUint16(dst[2:], flags)
		copy(dst[12:], request.Question.Name.Wire[:request.Question.Name.Length])
		table := blob[int(x.keyLen)+int(x.messageLen):]
		for j := 0; j < int(x.patches); j++ {
			p := table[j*8:]
			off, ttl := binary.BigEndian.Uint16(p), binary.BigEndian.Uint32(p[4:])
			left := uint32(0)
			if elapsed < uint64(ttl) {
				left = ttl - uint32(elapsed)
			}
			binary.BigEndian.PutUint32(dst[off:], left)
		}
		x.flags |= referenced
		return CacheResult{Hit: true, Negative: x.flags&negative != 0, Length: int(x.messageLen)}
	}
	return CacheResult{}
}

// Stats returns a bounded value snapshot. Shards are sampled independently.
func (c *Cache) Stats() Stats {
	out := Stats{RetainedBytes: c.retained, ShardCount: len(c.shards)}
	for i := range c.shards {
		s := &c.shards[i]
		s.mu.Lock()
		out.Shards[i] = ShardStats{s.entries, s.occupied, len(s.arena), len(s.slots), s.evictions}
		s.mu.Unlock()
	}
	return out
}
