package dnscache

import "time"

// Ticks preserve time.Time's monotonic clock when supplied by time.Now. Tests
// can advance a captured time without sleeping. Backward time is a cache miss;
// subtraction saturates before conversion and never wraps a uint32 TTL.
func (c *Cache) tick(now time.Time) int64 { return int64(now.Sub(c.epoch)) }
func age(now, inserted int64) (uint64, bool) {
	if now < inserted {
		return 0, false
	}
	return (uint64(now) - uint64(inserted)) / uint64(time.Second), true
}

func (s *shard) evict() bool {
	for scans := 0; scans < 2*len(s.slots); scans++ {
		i := s.hand
		s.hand = (s.hand + 1) % len(s.slots)
		x := &s.slots[i]
		if x.allocated == 0 {
			continue
		}
		if x.flags&referenced != 0 {
			x.flags &^= referenced
			continue
		}
		s.remove(i)
		s.evictions++
		return true
	}
	return false
}
