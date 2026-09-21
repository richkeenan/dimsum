package dnswire

import (
	"encoding/binary"
	"errors"
)

var (
	ErrQuestionCount = errors.New("dnswire: expected exactly one question")
	ErrResponse      = errors.New("dnswire: response on request path")
	ErrOpcode        = errors.New("dnswire: unsupported opcode")
	ErrClass         = errors.New("dnswire: unsupported question class")
	ErrUnsupported   = errors.New("dnswire: transfer or authentication unsupported")
)

const (
	FlagQR uint16 = 0x8000
	FlagAA uint16 = 0x0400
	FlagTC uint16 = 0x0200
	FlagRD uint16 = 0x0100
	FlagRA uint16 = 0x0080
	FlagAD uint16 = 0x0020
	FlagCD uint16 = 0x0010
)

type Header struct {
	ID, Flags                                    uint16
	Questions, Answers, Authorities, Additionals uint16
}

func ParseHeader(msg []byte) (Header, error) {
	if len(msg) < 12 || len(msg) > MaxMessageLen {
		return Header{}, ErrBounds
	}
	return Header{u16(msg, 0), u16(msg, 2), u16(msg, 4), u16(msg, 6), u16(msg, 8), u16(msg, 10)}, nil
}

func u16(p []byte, off int) uint16 { return binary.BigEndian.Uint16(p[off : off+2]) }

// Question has a copied expanded Name and a borrowed Original (encoded question
// including QTYPE/QCLASS). Original may contain compression pointers and must
// not be relocated without rebuilding from Name.Wire. Start/End are message
// offsets. Parsing the question alone does NOT validate the remaining message.
type Question struct {
	Header      Header
	Name        Name
	Type, Class uint16
	Start, End  int
	Original    []byte
}

// ParseQuestion accepts either request or response headers; policy validation
// is separate so upstream consumers can compare the same canonical question.
func ParseQuestion(msg []byte, out *Question) error {
	*out = Question{}
	h, err := ParseHeader(msg)
	if err != nil {
		return err
	}
	out.Header = h
	if h.Questions != 1 {
		return ErrQuestionCount
	}
	if err := DecodeName(msg, 12, &out.Name); err != nil {
		return err
	}
	off := out.Name.End
	if len(msg)-off < 4 {
		return ErrBounds
	}
	out.Type, out.Class = u16(msg, off), u16(msg, off+2)
	out.Start, out.End = 12, off+4
	out.Original = msg[out.Start:out.End:out.End]
	return nil
}

// RequestError checks the supported client question contract. ErrOpcode maps
// to NOTIMP; ErrClass/ErrUnsupported to REFUSED; ErrResponse must be dropped.
// It does not decide RD=0 misses or validate additional records; ParseRequest
// validates the complete request. All header flag bits remain available.
func (q *Question) RequestError() error {
	if q.Header.Flags&FlagQR != 0 {
		return ErrResponse
	}
	if q.Header.Flags&0x7800 != 0 {
		return ErrOpcode
	}
	if q.Class != 1 {
		return ErrClass
	}
	switch q.Type {
	case 249, 250, 251, 252:
		return ErrUnsupported
	}
	return nil
}
