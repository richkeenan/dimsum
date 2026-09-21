package dnswire

import "encoding/binary"

// SyntheticRecord contains only uncompressed, independently encoded local data.
// Never copy opaque upstream RDATA into this encoder.
type SyntheticRecord struct {
	Name []byte
	Type uint16
	TTL  uint32
	Data []byte
}

func BuildSynthetic(dst []byte, request *Message, rcode uint16, authoritative bool, answers, authority []SyntheticRecord) (int, error) {
	// BuildReply puts OPT last; insert records before it without relocating any names.
	n, err := BuildReply(dst, request, Reply{RCode: rcode, RecursionAvailable: true}, 1232)
	if err != nil {
		return 0, err
	}
	off := n
	if request.EDNS.Present {
		off -= 11
	}
	size := n
	for _, section := range [][]SyntheticRecord{answers, authority} {
		for _, r := range section {
			size += len(r.Name) + 10 + len(r.Data)
		}
	}
	if size > len(dst) || size > MaxMessageLen {
		return 0, ErrBounds
	}
	if request.EDNS.Present {
		copy(dst[size-11:size], dst[off:n])
	}
	for _, section := range [][]SyntheticRecord{answers, authority} {
		for _, r := range section {
			copy(dst[off:], r.Name)
			off += len(r.Name)
			put16(dst, off, r.Type)
			put16(dst, off+2, 1)
			binary.BigEndian.PutUint32(dst[off+4:], r.TTL)
			put16(dst, off+8, uint16(len(r.Data)))
			off += 10
			copy(dst[off:], r.Data)
			off += len(r.Data)
		}
	}
	put16(dst, 6, uint16(len(answers)))
	put16(dst, 8, uint16(len(authority)))
	if authoritative {
		put16(dst, 2, binary.BigEndian.Uint16(dst[2:])|0x0400)
	}
	return size, nil
}

// NegativeSOA supplies a valid negative-cache lifetime, including at the root.
func NegativeSOA(zone []byte, ttl uint32) SyntheticRecord {
	data := append([]byte{2, 'n', 's'}, zone...)
	data = append(data, 10)
	data = append(data, []byte("hostmaster")...)
	data = append(data, zone...)
	fields := make([]byte, 20)
	binary.BigEndian.PutUint32(fields, 1)
	binary.BigEndian.PutUint32(fields[4:], 3600)
	binary.BigEndian.PutUint32(fields[8:], 600)
	binary.BigEndian.PutUint32(fields[12:], 86400)
	binary.BigEndian.PutUint32(fields[16:], ttl)
	return SyntheticRecord{Name: zone, Type: 6, TTL: ttl, Data: append(data, fields...)}
}
