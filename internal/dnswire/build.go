package dnswire

import "encoding/binary"

// Reply describes synthetic data. Null returns zero-address A/AAAA answers and
// NODATA for other types. Synthesized replies never assert AA or AD.
type Reply struct {
	RCode              uint16
	Null               bool
	TTL                uint32
	RecursionAvailable bool
	Truncated          bool
}

func serverCap(n int) int {
	if n < 512 {
		return 512
	}
	if n > MaxMessageLen {
		return MaxMessageLen
	}
	return n
}

// UDPBudget applies the RFC minimum and local cap; absent EDNS always means 512.
func UDPBudget(e EDNS, cap int) int {
	if !e.Present {
		return 512
	}
	n := int(e.UDPSize)
	if n < 512 {
		n = 512
	}
	if n > serverCap(cap) {
		n = serverCap(cap)
	}
	return n
}

// BuildReply writes only into dst, rebuilding the copied expanded question.
// Request metadata must come from successful parsing (or ErrBadVersion).
// EDNS options are intentionally not echoed. dst may alias the original packet.
func BuildReply(dst []byte, request *Message, reply Reply, udpCap int) (int, error) {
	q := &request.Question
	if q.Name.Length < 1 || q.Name.Length > 255 || reply.RCode > 4095 || (reply.RCode > 15 && !request.EDNS.Present) {
		return 0, ErrRecord
	}
	end := 12 + int(q.Name.Length) + 4
	size := end
	data := 0
	if reply.Null && reply.RCode == 0 && !reply.Truncated && q.Class == 1 {
		switch q.Type {
		case 1:
			data = 4
		case 28:
			data = 16
		}
	}
	if data > 0 {
		size += 12 + data
	}
	if request.EDNS.Present {
		size += 11
	}
	if len(dst) < size {
		return 0, ErrBounds
	}
	clear(dst[:size])
	put16(dst, 0, q.Header.ID)
	flags := FlagQR | q.Header.Flags&(FlagRD|FlagCD|0x7800) | reply.RCode&15
	if reply.RecursionAvailable {
		flags |= FlagRA
	}
	if reply.Truncated {
		flags |= FlagTC
	}
	put16(dst, 2, flags)
	put16(dst, 4, 1)
	copy(dst[12:], q.Name.Wire[:q.Name.Length])
	put16(dst, end-4, q.Type)
	put16(dst, end-2, q.Class)
	off := end
	if data > 0 {
		put16(dst, 6, 1)
		put16(dst, off, 0xc00c)
		put16(dst, off+2, q.Type)
		put16(dst, off+4, 1)
		binary.BigEndian.PutUint32(dst[off+6:], reply.TTL)
		put16(dst, off+10, uint16(data))
		off += 12 + data
	}
	if request.EDNS.Present {
		put16(dst, 10, 1)
		put16(dst, off+1, 41)
		put16(dst, off+3, uint16(serverCap(udpCap)))
		dst[off+5] = byte(reply.RCode >> 4)
		if request.EDNS.DO {
			put16(dst, off+7, 0x8000)
		}
	}
	return size, nil
}

func put16(p []byte, off int, v uint16) { binary.BigEndian.PutUint16(p[off:off+2], v) }

// FitReply validates before copying. Oversized replies are reconstructed as a
// question-only TC response: no record is relocated or cut, including unknown
// RDATA with possible compression. Full replies remain byte-preserving. dst may
// alias response. Authenticity and matching the client belong to the resolver.
func FitReply(dst, response []byte, request *Message, budget, udpCap int) (int, error) {
	var m Message
	if err := ScanMessage(response, &m); err != nil {
		return 0, err
	}
	if m.Question.Header.Flags&FlagQR == 0 {
		return 0, ErrRecord
	}
	if len(response) <= budget {
		if len(dst) < len(response) {
			return 0, ErrBounds
		}
		return copy(dst, response), nil
	}
	n, err := BuildReply(dst, request, Reply{RCode: m.RCode, Truncated: true, RecursionAvailable: m.Question.Header.Flags&FlagRA != 0}, udpCap)
	if err != nil {
		return 0, err
	}
	if n > budget {
		return 0, ErrBounds
	}
	return n, nil
}
