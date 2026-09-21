package dnswire

import (
	"encoding/binary"
	"errors"
)

var (
	ErrRecord     = errors.New("dnswire: malformed record data")
	ErrEDNS       = errors.New("dnswire: malformed EDNS")
	ErrBadVersion = errors.New("dnswire: unsupported EDNS version")
	ErrTrailing   = errors.New("dnswire: bytes after declared records")
)

type Section uint8

const (
	Answer Section = iota
	Authority
	Additional
)

// Record describes original message offsets. RData borrows the input and must
// remain at its original message offset unless its type is understood by a
// future encoder. Unknown types are deliberately opaque. TTL is the raw field;
// TTLOffset is -1 for OPT, whose field is metadata rather than an expiring TTL.
type Record struct {
	Name                              Name
	Type, Class                       uint16
	TTL                               uint32
	Section                           Section
	Start, TTLOffset, DataOffset, End int
	RData                             []byte
}

// EDNS retains raw advertised size (including values below 512), flags, and
// option bytes. Applying response budgets and option forwarding/cache policy
// belongs to consumers. Options is borrowed, validated TLV data; option-specific
// payload semantics are not interpreted. Nonempty options require general-path
// handling rather than blind replay in a cache template.
type EDNS struct {
	Present, DO            bool
	UDPSize, Flags         uint16
	Version, ExtendedRCode uint8
	RecordOffset           int
	Options                []byte
}

type Option struct {
	Code uint16
	Data []byte
}

// ReadOption reads one TLV at off and returns a strictly advancing next offset.
// Iterate while off < len(options); an empty/end input returns ErrEDNS.
func ReadOption(options []byte, off int) (Option, int, error) {
	if off < 0 || off > len(options) || len(options)-off < 4 {
		return Option{}, off, ErrEDNS
	}
	n := int(u16(options, off+2))
	end := off + 4 + n
	if end > len(options) {
		return Option{}, off, ErrEDNS
	}
	return Option{u16(options, off), options[off+4 : end : end]}, end, nil
}

// Scanner walks declared records without allocating a record graph. Init only
// parses the question. Next validates each record, including understood embedded
// names and OPT. The entire message is validated ONLY after Next returns false
// and Err returns nil. Earlier records/EDNS must not be trusted as a complete
// response. Work is bounded by 65535 message bytes and DecodeName's limits.
// A Scanner borrows its input; do not mutate it during iteration.
type Scanner struct {
	Question Question
	EDNS     EDNS
	msg      []byte
	// reference enables decoded-name comparison during layout-preserving
	// personalization. It is nil for ordinary parsing.
	reference []byte
	off       int
	counts    [3]uint16
	section   Section
	err       error
}

func (s *Scanner) Init(msg []byte) error {
	*s = Scanner{msg: msg}
	s.err = ParseQuestion(msg, &s.Question)
	if s.err != nil {
		return s.err
	}
	h := s.Question.Header
	s.counts = [3]uint16{h.Answers, h.Authorities, h.Additionals}
	s.off = s.Question.End
	return nil
}

func (s *Scanner) Err() error { return s.err }

func (s *Scanner) Next(out *Record) bool {
	if s.err != nil {
		return false
	}
	for s.section <= Additional && s.counts[s.section] == 0 {
		s.section++
	}
	if s.section > Additional {
		if s.off != len(s.msg) {
			s.err = ErrTrailing
		}
		return false
	}
	var r Record
	r.Start, r.Section = s.off, s.section
	if s.err = decodeNameCompared(s.msg, s.reference, s.off, &r.Name); s.err != nil {
		return false
	}
	off := r.Name.End
	if len(s.msg)-off < 10 {
		s.err = ErrBounds
		return false
	}
	r.Type, r.Class = u16(s.msg, off), u16(s.msg, off+2)
	r.TTL = binary.BigEndian.Uint32(s.msg[off+4 : off+8])
	r.TTLOffset, r.DataOffset = off+4, off+10
	r.End = r.DataOffset + int(u16(s.msg, off+8))
	if r.End > len(s.msg) {
		s.err = ErrBounds
		return false
	}
	r.RData = s.msg[r.DataOffset:r.End:r.End]
	if r.Type == 41 {
		r.TTLOffset = -1
		s.err = s.readOPT(&r)
	} else {
		s.err = validateRData(s.msg, s.reference, &r)
	}
	if s.err != nil {
		return false
	}
	s.off = r.End
	s.counts[s.section]--
	*out = r
	return true
}

func (s *Scanner) readOPT(r *Record) error {
	if s.EDNS.Present || r.Section != Additional || r.Name.Length != 1 || r.Name.Compressed {
		return ErrEDNS
	}
	for off := 0; off < len(r.RData); {
		_, next, err := ReadOption(r.RData, off)
		if err != nil {
			return err
		}
		off = next
	}
	s.EDNS = EDNS{Present: true, DO: r.TTL&0x8000 != 0, UDPSize: r.Class, Flags: uint16(r.TTL), Version: uint8(r.TTL >> 16), ExtendedRCode: uint8(r.TTL >> 24), RecordOffset: r.Start, Options: r.RData}
	return nil
}

// Message is a validated envelope and copied question, with borrowed original
// question/EDNS bytes. It does not authenticate an upstream endpoint or ID.
type Message struct {
	Question          Question
	EDNS              EDNS
	RCode             uint16
	HasAuthentication bool
}

func ScanMessage(msg []byte, out *Message) error {
	*out = Message{}
	var s Scanner
	if err := s.Init(msg); err != nil {
		return err
	}
	var rr Record
	auth := false
	for s.Next(&rr) {
		if rr.Type == 249 || rr.Type == 250 {
			auth = true
		}
	}
	if s.Err() != nil {
		return s.Err()
	}
	*out = Message{Question: s.Question, EDNS: s.EDNS, RCode: uint16(s.EDNS.ExtendedRCode)<<4 | s.Question.Header.Flags&15, HasAuthentication: auth}
	return nil
}

// ParseRequest validates the complete client message and supported protocol.
// ErrBadVersion maps to extended RCODE BADVERS (16); metadata remains available
// to build that reply. Structural errors map to FORMERR when a safe reply can
// be built. A header shorter than 12 bytes and ErrResponse require dropping.
func ParseRequest(msg []byte, out *Message) error {
	*out = Message{}
	h, err := ParseHeader(msg)
	if err != nil {
		return err
	}
	if h.Flags&FlagQR != 0 {
		return ErrResponse
	}
	if h.Flags&0x7800 != 0 {
		return ErrOpcode
	}
	if err := ScanMessage(msg, out); err != nil {
		return err
	}
	if err := out.Question.RequestError(); err != nil {
		return err
	}
	if out.HasAuthentication {
		return ErrUnsupported
	}
	if out.EDNS.Version != 0 {
		return ErrBadVersion
	}
	if out.EDNS.ExtendedRCode != 0 {
		return ErrEDNS
	}
	return nil
}

// rdataName bounds the encoded occurrence to RDLENGTH while allowing legal
// compression targets elsewhere in the message. Some newer RR types prohibit
// compression; their original Name.Compressed is checked explicitly.
func rdataName(msg, reference []byte, off, end int, compression bool) (int, error) {
	if off >= end {
		return off, ErrRecord
	}
	var n Name
	if err := decodeNameCompared(msg, reference, off, &n); err != nil {
		return off, err
	}
	if n.End > end || (!compression && n.Compressed) {
		return off, ErrRecord
	}
	return n.End, nil
}

func validateRData(msg, reference []byte, r *Record) error {
	off, end := r.DataOffset, r.End
	nameCount, prefix, tail, compression := 0, 0, 0, true
	switch r.Type {
	case 1:
		if end-off != 4 {
			return ErrRecord
		}
		return nil
	case 28:
		if end-off != 16 {
			return ErrRecord
		}
		return nil
	case 2, 5, 12:
		nameCount = 1
	case 39:
		nameCount = 1
		compression = false
	case 15:
		nameCount = 1
		prefix = 2
	case 33:
		nameCount = 1
		prefix = 6
		compression = false
	case 6:
		nameCount = 2
		tail = 20
	case 16:
		if off == end {
			return ErrRecord
		}
		for off < end {
			off += 1 + int(msg[off])
			if off > end {
				return ErrRecord
			}
		}
		return nil
	case 64, 65:
		if end-off < 3 {
			return ErrRecord
		}
		next, err := rdataName(msg, reference, off+2, end, false)
		if err != nil {
			return err
		}
		last := -1
		for next < end {
			if end-next < 4 {
				return ErrRecord
			}
			key := int(u16(msg, next))
			n := int(u16(msg, next+2))
			if key <= last || n > end-next-4 {
				return ErrRecord
			}
			last = key
			next += 4 + n
		}
		return nil
	case 46:
		if end-off < 20 {
			return ErrRecord
		}
		next, err := rdataName(msg, reference, off+18, end, false)
		if err != nil {
			return err
		}
		if next == end {
			return ErrRecord
		}
		return nil
	case 47:
		next, err := rdataName(msg, reference, off, end, false)
		if err != nil {
			return err
		}
		last := -1
		if next == end {
			return ErrRecord
		}
		for next < end {
			if end-next < 2 {
				return ErrRecord
			}
			window, n := int(msg[next]), int(msg[next+1])
			if window <= last || n < 1 || n > 32 || n > end-next-2 {
				return ErrRecord
			}
			if msg[next+1+n] == 0 {
				return ErrRecord
			}
			last = window
			next += 2 + n
		}
		return nil
	default:
		// Unknown and other opaque records are envelope-validated only. Never
		// infer names by looking for pointer-shaped bytes in unknown RDATA.
		return nil
	}
	if end-off < prefix {
		return ErrRecord
	}
	off += prefix
	for i := 0; i < nameCount; i++ {
		next, err := rdataName(msg, reference, off, end, compression)
		if err != nil {
			return err
		}
		off = next
	}
	if end-off != tail {
		return ErrRecord
	}
	return nil
}
