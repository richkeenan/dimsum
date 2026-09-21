package upstream

import (
	"crypto/rand"
	"encoding/binary"
	"sync"
	"time"
)

func random16() (uint16, error) {
	var b [2]byte
	_, err := rand.Read(b[:])
	return binary.BigEndian.Uint16(b[:]), err
}

// IDs are unique across live sockets and quarantined after completion. Together
// with a fresh random source port this prevents immediate tuple reuse. The table
// is fixed size; exhaustion fails closed rather than reusing a recent ID.
type ids struct {
	mu     sync.Mutex
	until  [65536]time.Time
	active [65536]bool
}

func (p *ids) acquire() (uint16, error) {
	start, err := random16()
	if err != nil {
		return 0, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	for i := 0; i < 65536; i++ {
		id := start + uint16(i)
		if !p.active[id] && !p.until[id].After(now) {
			p.active[id] = true
			return id, nil
		}
	}
	return 0, ErrOverloaded
}
func (p *ids) release(id uint16, quarantine time.Duration) {
	p.mu.Lock()
	p.until[id] = time.Now().Add(quarantine)
	p.active[id] = false
	p.mu.Unlock()
}
