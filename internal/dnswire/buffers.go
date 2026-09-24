package dnswire

// MessageBuffers retains a bounded number of full-size DNS scratch buffers.
// Outstanding borrows are bounded separately by the caller's admission limit.
// Get never blocks. A returned buffer is not zeroed: use only bytes written by
// the current exchange, and never retain a slice after returning it with Put.
// The zero value works without retaining any returned buffers.
type MessageBuffers struct {
	free chan *[MaxMessageLen]byte
}

func NewMessageBuffers(retained int) MessageBuffers {
	return MessageBuffers{free: make(chan *[MaxMessageLen]byte, retained)}
}

func (p *MessageBuffers) Get() *[MaxMessageLen]byte {
	select {
	case b := <-p.free:
		return b
	default:
		return new([MaxMessageLen]byte)
	}
}

func (p *MessageBuffers) Put(b *[MaxMessageLen]byte) {
	select {
	case p.free <- b:
	default:
	}
}
