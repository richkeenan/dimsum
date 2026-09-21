package policy

import "fmt"

// Temporary fixed-size pages avoid repeatedly copying a growing exact-key
// arena. Keys never straddle pages; finalization produces the existing contiguous
// representation, so runtime lookup gains no additional indirection or branch.
const exactBuildPageBytes = 64 << 10

type exactBuildArena struct{ pages [][]byte }

func (a *exactBuildArena) size() uint64 {
	if len(a.pages) == 0 {
		return 0
	}
	return uint64(len(a.pages)-1)*exactBuildPageBytes + uint64(len(a.pages[len(a.pages)-1]))
}

func (a *exactBuildArena) add(key string, limit uint64) (uint32, error) {
	if len(key) > 254 {
		return 0, fmt.Errorf("policy: exact key too long")
	}
	need := len(key) + 1
	start := a.size()
	newPage := len(a.pages) == 0 || len(a.pages[len(a.pages)-1])+need > exactBuildPageBytes
	if newPage {
		start = uint64(len(a.pages)) * exactBuildPageBytes
	}
	if start+uint64(need) > limit {
		return 0, fmt.Errorf("policy: exact key budget exceeded")
	}
	if newPage {
		a.pages = append(a.pages, make([]byte, 0, exactBuildPageBytes))
	}
	i := len(a.pages) - 1
	a.pages[i] = append(a.pages[i], byte(len(key)))
	a.pages[i] = append(a.pages[i], key...)
	return uint32(start), nil
}

func (a *exactBuildArena) key(off uint32) string {
	page := a.pages[off/exactBuildPageBytes]
	i := off % exactBuildPageBytes
	return string(page[i+1 : i+1+uint32(page[i])])
}

func (a *exactBuildArena) finish() []byte {
	if len(a.pages) == 0 {
		return nil
	}
	keys := make([]byte, a.size())
	for i, page := range a.pages {
		copy(keys[i*exactBuildPageBytes:], page)
	}
	return keys
}

func (x *exactIndex) insertStaged(key string, hash uint64, head uint32, a *exactBuildArena, limit uint64) (uint32, error) {
	for i := hash % uint64(len(x.slots)); ; i = (i + 1) % uint64(len(x.slots)) {
		p := &x.slots[i]
		if p.head == 0 {
			off, err := a.add(key, limit)
			if err != nil {
				return 0, err
			}
			*p = exactSlot{hash: hash, off: off, head: head}
			return 0, nil
		}
		if p.hash == hash && a.key(p.off) == key {
			old := p.head
			p.head = head
			return old, nil
		}
	}
}
