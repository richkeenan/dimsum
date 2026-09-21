package upstream

import (
	"encoding/binary"
	"errors"

	"github.com/richkeenan/dimsum/internal/dnswire"
)

var ErrResponse = errors.New("upstream: invalid or mismatched response")

// Validation is the only gate to ExchangeResult. A compressed response question
// cannot be patched safely while retaining opaque RDATA, so fail closed on that
// uncommon layout. Outgoing questions are always expanded.
func validate(wire []byte, q *dnswire.Question, id uint16) (dnswire.Message, error) {
	var m dnswire.Message
	if err := dnswire.ScanMessage(wire, &m); err != nil {
		return m, ErrResponse
	}
	r := &m.Question
	if r.Header.ID != id || r.Header.Flags&dnswire.FlagQR == 0 || r.Header.Flags&0x7800 != q.Header.Flags&0x7800 || r.Type != q.Type || r.Class != q.Class || r.Name.Length != q.Name.Length || r.Name.Canonical != q.Name.Canonical || r.Name.Compressed || m.HasAuthentication || m.EDNS.Version != 0 {
		return m, ErrResponse
	}
	for off := 0; off < len(m.EDNS.Options); {
		opt, next, err := dnswire.ReadOption(m.EDNS.Options, off)
		if err != nil || opt.Code == 8 || opt.Code == 10 {
			return m, ErrResponse
		}
		off = next
	}
	return m, nil
}

// Normalize the question without relocating records. Client COOKIE and ECS are
// deliberately not sent to another endpoint. Other options take the uncached
// forwarding path. Only ordinary queries with an optional OPT are supported.
func prepare(wire []byte) ([]byte, dnswire.Question, error) {
	var m dnswire.Message
	if err := dnswire.ParseRequest(wire, &m); err != nil {
		return nil, m.Question, err
	}
	q := m.Question
	h := q.Header
	expected := uint16(0)
	if m.EDNS.Present {
		expected = 1
	}
	if h.Answers != 0 || h.Authorities != 0 || h.Additionals != expected {
		return nil, q, dnswire.ErrUnsupported
	}
	p := make([]byte, 12, len(wire)+255)
	binary.BigEndian.PutUint16(p[2:4], h.Flags&(dnswire.FlagRD|dnswire.FlagCD))
	p[5] = 1
	p = append(p, q.Name.Wire[:q.Name.Length]...)
	p = binary.BigEndian.AppendUint16(p, q.Type)
	p = binary.BigEndian.AppendUint16(p, q.Class)
	if m.EDNS.Present {
		p[11] = 1
		p = append(p, 0, 0, 41, 4, 208, 0, 0, 0, 0, 0, 0) // 1232-byte upstream UDP budget
		if m.EDNS.DO {
			p[len(p)-4] = 0x80
		}
		length := len(p) - 2
		start := len(p)
		for off := 0; off < len(m.EDNS.Options); {
			opt, next, err := dnswire.ReadOption(m.EDNS.Options, off)
			if err != nil {
				return nil, q, err
			}
			if opt.Code != 8 && opt.Code != 10 {
				p = append(p, m.EDNS.Options[off:next]...)
			}
			off = next
		}
		binary.BigEndian.PutUint16(p[length:], uint16(len(p)-start))
	}
	if len(p) > 65535 {
		return nil, q, dnswire.ErrBounds
	}
	return p, q, nil
}
