package dnswire

import "bytes"

// SyntheticSections re-encodes understood records when joining an upstream
// target to a local CNAME chain. Unknown RDATA cannot safely change offsets;
// reject that uncommon continuation rather than corrupting opaque bytes. Normal
// forwarding continues to use its byte-preserving path. Additional records and
// DNSSEC signatures/denial proofs are not inherited by synthesized local data.
func SyntheticSections(message []byte) (answers, authority []SyntheticRecord, err error) {
	var s Scanner
	if err = s.Init(message); err != nil {
		return
	}
	var rr Record
	for s.Next(&rr) {
		if rr.Section == Additional || rr.Type == 46 || rr.Type == 47 || rr.Type == 50 {
			continue
		}
		if rr.Class != 1 {
			return nil, nil, ErrUnsupported
		}
		r := SyntheticRecord{Name: bytes.Clone(rr.Name.Wire[:rr.Name.Length]), Type: rr.Type, TTL: rr.TTL}
		names, prefix, tail := 0, 0, 0
		switch rr.Type {
		case 1, 28, 16, 64, 65:
			r.Data = bytes.Clone(rr.RData)
		case 2, 5, 12, 39:
			names = 1
		case 6:
			names = 2
			tail = 20
		case 15:
			names = 1
			prefix = 2
		case 33:
			names = 1
			prefix = 6
		default:
			return nil, nil, ErrUnsupported
		}
		if names > 0 {
			r.Data = bytes.Clone(rr.RData[:prefix])
			off := rr.DataOffset + prefix
			for range names {
				var name Name
				if e := DecodeName(message, off, &name); e != nil {
					return nil, nil, e
				}
				r.Data = append(r.Data, name.Wire[:name.Length]...)
				off = name.End
			}
			r.Data = append(r.Data, message[off:off+tail]...)
		}
		if rr.Section == Answer {
			answers = append(answers, r)
		} else {
			authority = append(authority, r)
		}
	}
	if s.Err() != nil {
		return nil, nil, s.Err()
	}
	return
}
