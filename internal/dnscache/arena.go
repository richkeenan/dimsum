package dnscache

const blockSize = 256

// A bitmap allocator keeps templates contiguous, with at most 255 bytes of
// internal fragmentation per blob. Fragmentation and unused arena capacity
// are both charged in full at construction, not just occupied payload bytes.
func (s *shard) allocate(size int) (int, int, bool) {
	need := (size + blockSize - 1) / blockSize
	blocks := len(s.arena) / blockSize
	for pass := 0; pass < 2; pass++ {
		start, end := s.cursor, blocks
		if pass == 1 {
			start, end = 0, s.cursor
		}
		run := 0
		for i := start; i < end; i++ {
			if s.used[i/64]&(uint64(1)<<uint(i%64)) != 0 {
				run = 0
				continue
			}
			run++
			if run != need {
				continue
			}
			first := i + 1 - need
			for j := first; j <= i; j++ {
				s.used[j/64] |= uint64(1) << uint(j%64)
			}
			s.cursor = (i + 1) % blocks
			s.occupied += need * blockSize
			return first * blockSize, need * blockSize, true
		}
	}
	// A free run crossing the search cursor can be missed above; retry once
	// from zero before asking CLOCK to evict any entry.
	if s.cursor != 0 {
		s.cursor = 0
		return s.allocate(size)
	}
	return 0, 0, false
}

func (s *shard) remove(i int) {
	x := &s.slots[i]
	if x.allocated == 0 {
		return
	}
	first, count := int(x.offset)/blockSize, int(x.allocated)/blockSize
	for j := first; j < first+count; j++ {
		s.used[j/64] &^= uint64(1) << uint(j%64)
	}
	s.occupied -= int(x.allocated)
	s.entries--
	*x = slot{}
}
