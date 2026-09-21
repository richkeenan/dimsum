// Package dnswire parses DNS messages without retaining or mutating caller storage
// except for explicitly documented borrowed slices. Callers must keep borrowed
// messages immutable and alive until all consumers finish. Outputs are usable
// only on success. Names are copied values; no wire labels are treated as UTF-8.
package dnswire

import "errors"

const (
	MaxNameLen = 255
	// MaxPointers bounds work independently of the expanded name length.
	MaxPointers   = 32
	MaxMessageLen = 65535
)

var (
	ErrBounds      = errors.New("dnswire: truncated or oversized input")
	ErrLabel       = errors.New("dnswire: unsupported label encoding")
	ErrPointer     = errors.New("dnswire: invalid or excessive compression")
	ErrNameTooLong = errors.New("dnswire: expanded name exceeds 255 octets")
)

// Name contains uncompressed, length-prefixed wire names including the root.
// Wire preserves case and arbitrary bytes; Canonical folds ASCII A-Z only.
// End is the offset following the encoded name in the original message, not
// following a compression target. Copying a Name transfers no borrowed storage.
type Name struct {
	Wire       [MaxNameLen]byte
	Canonical  [MaxNameLen]byte
	Length     uint16
	End        int
	Compressed bool
}

// DecodeName validates a name at off into caller-owned out. RFC 1035 pointers
// must reference a prior occurrence (forward pointers are rejected). Targets
// are decoded structurally; this does not prove they reference a known field
// boundary in an otherwise opaque message. Cycles and long chains are rejected.
func DecodeName(msg []byte, off int, out *Name) error {
	*out = Name{}
	if len(msg) > MaxMessageLen {
		return ErrBounds
	}
	var visited [MaxPointers]int
	jumps, n := 0, 0
	end := -1
	for {
		if off < 0 || off >= len(msg) {
			return ErrBounds
		}
		c := msg[off]
		switch c & 0xc0 {
		case 0xc0:
			if off+1 >= len(msg) {
				return ErrBounds
			}
			target := int(c&0x3f)<<8 | int(msg[off+1])
			if target >= off || jumps == MaxPointers {
				return ErrPointer
			}
			for _, seen := range visited[:jumps] {
				if seen == off {
					return ErrPointer
				}
			}
			visited[jumps] = off
			jumps++
			if end < 0 {
				end = off + 2
			}
			out.Compressed = true
			off = target
		case 0:
			length := int(c)
			if off+1+length > len(msg) {
				return ErrBounds
			}
			if n+1+length > MaxNameLen || (length != 0 && n+1+length == MaxNameLen) {
				return ErrNameTooLong
			}
			out.Wire[n], out.Canonical[n] = c, c
			n++
			for _, b := range msg[off+1 : off+1+length] {
				out.Wire[n] = b
				if b >= 'A' && b <= 'Z' {
					b += 'a' - 'A'
				}
				out.Canonical[n] = b
				n++
			}
			off += 1 + length
			if length == 0 {
				if end < 0 {
					end = off
				}
				out.Length, out.End = uint16(n), end
				return nil
			}
		default:
			return ErrLabel
		}
	}
}
