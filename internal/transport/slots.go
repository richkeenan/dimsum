// Package transport provides bounded client DNS transports. All handler borrows
// end at return; neither request nor output storage may escape to async work.
package transport

import "time"

const smallSize = 2048
const largeSize = 65535

// RequestSlot is exclusively owned by admission, then a worker, then the pool.
// It is deliberately not exposed to handlers, which cannot release its storage.
type RequestSlot struct {
	wire     []byte
	large    bool
	deadline time.Time
}
type slots struct {
	small, large chan *RequestSlot
	storage      int
}

func newSlots(small, large int) *slots {
	p := &slots{small: make(chan *RequestSlot, small), large: make(chan *RequestSlot, large), storage: small*smallSize + large*largeSize}
	for range small {
		p.small <- &RequestSlot{wire: make([]byte, smallSize)}
	}
	for range large {
		p.large <- &RequestSlot{wire: make([]byte, largeSize), large: true}
	}
	return p
}
func (p *slots) acquire(n int) *RequestSlot {
	if n < 0 || n > largeSize {
		return nil
	}
	ch := p.small
	if n > smallSize {
		ch = p.large
	}
	select {
	case s := <-ch:
		return s
	default:
		return nil
	}
}
func (p *slots) release(s *RequestSlot) {
	s.wire = s.wire[:cap(s.wire)]
	if s.large {
		p.large <- s
	} else {
		p.small <- s
	}
}
func (p *slots) bytes() int { return p.storage }
