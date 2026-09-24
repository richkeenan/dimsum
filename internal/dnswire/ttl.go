package dnswire

import (
	"bytes"
	"encoding/binary"
	"errors"
)

var ErrTemplate = errors.New("dnswire: response not eligible for a cache template")

// nameBoundaries proves that every understood compression dependency ends in
// previously validated name fields, never headers, TTLs or opaque RDATA. Mark
// only label starts (not label contents). Induction over backward pointers then
// permits ID/flags/TTL changes and same-length question case changes without a
// per-hit parse. Unknown RDATA remains opaque at its original offset.
type nameBoundaries [1024]uint64

func (n *nameBoundaries) add(msg []byte, off int) error {
	for {
		b := msg[off]
		if b&0xc0 == 0xc0 {
			target := int(b&63)<<8 | int(msg[off+1])
			if n[target/64]&(uint64(1)<<uint(target%64)) == 0 {
				return ErrTemplate
			}
			n[off/64] |= uint64(1) << uint(off%64)
			return nil
		}
		n[off/64] |= uint64(1) << uint(off%64)
		if b == 0 {
			return nil
		}
		off += 1 + int(b)
	}
}

// Template describes a validated, layout-preserving response. Patches are
// eight-byte big-endian entries: offset uint16, two zero bytes, TTL uint32.
type Template struct {
	Lifetime uint32
	Patches  int
	Negative bool
}

// PrepareTemplate validates the entire message and emits TTL patches into
// caller storage. Outputs are usable only on success. Authentication of the
// upstream endpoint, request ID and policy provenance remains the caller's job.
// maxNegative must be nonzero; negative SOA TTLs are clamped per RFC 2308.
func PrepareTemplate(msg, patches []byte, maxNegative uint32) (Template, error) {
	var s Scanner
	if err := s.Init(msg); err != nil {
		return Template{}, err
	}
	q := &s.Question
	flags := q.Header.Flags
	if q.Name.Compressed || q.Class != 1 || flags&FlagQR == 0 || flags&(FlagTC|0x7800) != 0 || (flags&15 != 0 && flags&15 != 3) {
		return Template{}, ErrTemplate
	}
	var boundaries nameBoundaries
	if err := boundaries.add(msg, 12); err != nil {
		return Template{}, err
	}
	info := Template{Lifetime: ^uint32(0)}
	var rr Record
	positive, soa, cname := false, false, false
	// First pass establishes complete validity and whether this is a negative
	// answer. A CNAME-only answer without SOA is deliberately not cached.
	for s.next(&rr, &boundaries) {
		if rr.Type == 249 || rr.Type == 250 || (rr.Type != 41 && rr.Class != 1) {
			return Template{}, ErrTemplate
		}
		if rr.Section == Answer && (rr.Type == q.Type || q.Type == 255) {
			positive = true
		}
		if rr.Section == Answer && rr.Type == 5 {
			cname = true
		}
		if rr.Section == Authority && rr.Type == 6 && enclosing(&q.Name, &rr.Name) {
			soa = true
		}
	}
	if s.Err() != nil {
		return Template{}, s.Err()
	}
	if s.EDNS.Version != 0 || s.EDNS.ExtendedRCode != 0 || s.EDNS.Flags&^uint16(0x8000) != 0 || len(s.EDNS.Options) != 0 {
		return Template{}, ErrTemplate
	}
	info.Negative = flags&15 == 3 || !positive
	if info.Negative && cname {
		var err error
		soa, err = negativeCNAMEAuthority(msg, q.Name)
		if err != nil {
			return Template{}, err
		}
	}
	if info.Negative && (!soa || maxNegative == 0) {
		return Template{}, ErrTemplate
	}
	// The second pass uses the now-proven envelope and emits final TTL values.
	if err := s.Init(msg); err != nil {
		return Template{}, err
	}
	for s.Next(&rr) {
		if rr.TTLOffset < 0 {
			continue
		}
		ttl := rr.TTL
		// RFC 2181: high-bit TTLs are treated as zero.
		if ttl == 0 || ttl > 0x7fffffff {
			return Template{}, ErrTemplate
		}
		if info.Negative && rr.Type == 6 && rr.Section == Authority {
			ttl = min(ttl, binary.BigEndian.Uint32(msg[rr.End-4:rr.End]), maxNegative)
			if ttl == 0 {
				return Template{}, ErrTemplate
			}
		}
		if len(patches)-info.Patches*8 < 8 {
			return Template{}, ErrBounds
		}
		p := patches[info.Patches*8:][:8]
		binary.BigEndian.PutUint16(p, uint16(rr.TTLOffset))
		p[2], p[3] = 0, 0
		binary.BigEndian.PutUint32(p[4:], ttl)
		info.Patches++
		info.Lifetime = min(info.Lifetime, ttl)
	}
	if s.Err() != nil {
		return Template{}, s.Err()
	}
	if info.Patches == 0 {
		return Template{}, ErrTemplate
	}
	return info, nil
}

// negativeCNAMEAuthority checks the terminal CNAME name, not the original
// question, against the negative authority (RFC 2308 section 1). The caller has
// already validated the complete message and its compression dependencies.
// Rescanning handles unordered answers without retaining an allocated RR graph.
// The 16-link bound matches response-policy inspection; repeated record offsets
// detect cycles without storing another array of expanded names.
func negativeCNAMEAuthority(msg []byte, current Name) (bool, error) {
	var seen [16]int
	for links := 0; ; links++ {
		var s Scanner
		if err := s.Init(msg); err != nil {
			return false, err
		}
		var rr Record
		var next Name
		nextOffset := -1
		soa := false
		for s.Next(&rr) {
			if rr.Section == Authority && rr.Type == 6 && enclosing(&current, &rr.Name) {
				soa = true
			}
			if rr.Section != Answer || rr.Type != 5 || !bytes.Equal(current.Canonical[:current.Length], rr.Name.Canonical[:rr.Name.Length]) {
				continue
			}
			var target Name
			if err := DecodeName(msg, rr.DataOffset, &target); err != nil {
				return false, err
			}
			if nextOffset >= 0 {
				if !bytes.Equal(next.Canonical[:next.Length], target.Canonical[:target.Length]) {
					return false, ErrTemplate
				}
				continue
			}
			next, nextOffset = target, rr.Start
		}
		if err := s.Err(); err != nil {
			return false, err
		}
		if nextOffset < 0 {
			return soa, nil
		}
		if links == len(seen) {
			return false, ErrTemplate
		}
		for _, offset := range seen[:links] {
			if offset == nextOffset {
				return false, ErrTemplate
			}
		}
		seen[links] = nextOffset
		current = next
	}
}

func enclosing(q, zone *Name) bool {
	for off := 0; off < int(q.Length); off += 1 + int(q.Canonical[off]) {
		if bytes.Equal(q.Canonical[off:q.Length], zone.Canonical[:zone.Length]) {
			return true
		}
	}
	return false
}
