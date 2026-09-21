package dnswire

import (
	"bytes"
	"encoding/binary"
	"errors"
)

var ErrNameChanged = errors.New("dnswire: personalization changes name semantics or requires unsafe relocation")

// decodeNameCompared follows the complete pointer chain in both messages, not
// just the first target. Canonical equality permits question case restoration
// but disallows changed owner/target meaning, even if both names still parse.
func decodeNameCompared(msg, reference []byte, off int, out *Name) error {
	if err := DecodeName(msg, off, out); err != nil {
		return err
	}
	if reference != nil {
		var before Name
		if err := DecodeName(reference, off, &before); err != nil {
			return err
		}
		if out.End != before.End || out.Length != before.Length || out.Canonical != before.Canonical {
			return ErrNameChanged
		}
	}
	return nil
}

// PersonalizeReply patches a validated response for a client, then verifies all
// RR owners and understood embedded names against their original meanings.
// Unknown RDATA stays byte-for-byte at its original offset. Input/output may
// alias; output is changed only on success. Source/ID authenticity is upstream's
// responsibility. Compressed questions retain their encoding when safe; only
// record-free messages may be rebuilt with an expanded question. This is an
// uncached general path, not proof of template-cache eligibility. The temporary
// candidate is owned only for this call.
func PersonalizeReply(dst, response []byte, request *Message) (int, error) {
	var original Message
	if err := ScanMessage(response, &original); err != nil {
		return 0, err
	}
	q, want := &original.Question, &request.Question
	if q.Header.Flags&FlagQR == 0 || q.Type != want.Type || q.Class != want.Class || q.Name.Length != want.Name.Length || q.Name.Canonical != want.Name.Canonical {
		return 0, ErrNameChanged
	}
	flags := q.Header.Flags &^ (FlagAD | FlagRD | FlagCD)
	flags |= want.Header.Flags & (FlagRD | FlagCD)
	if q.Name.Compressed && q.Header.Answers == 0 && q.Header.Authorities == 0 && q.Header.Additionals == 0 {
		// Nothing follows the question, so expanding it cannot relocate RDATA
		// or change any RR name that references its old encoding.
		n := 12 + int(want.Name.Length) + 4
		if len(dst) < n {
			return 0, ErrBounds
		}
		candidate := make([]byte, n)
		copy(candidate, response[:12])
		binary.BigEndian.PutUint16(candidate, want.Header.ID)
		binary.BigEndian.PutUint16(candidate[2:4], flags)
		copy(candidate[12:], want.Name.Wire[:want.Name.Length])
		binary.BigEndian.PutUint16(candidate[n-4:], want.Type)
		binary.BigEndian.PutUint16(candidate[n-2:], want.Class)
		return copy(dst, candidate), nil
	}
	if len(dst) < len(response) {
		return 0, ErrBounds
	}
	candidate := bytes.Clone(response)
	binary.BigEndian.PutUint16(candidate, want.Header.ID)
	binary.BigEndian.PutUint16(candidate[2:4], flags)
	// Patch only literal label data in the encoded question, never arbitrary
	// compression targets in the header or elsewhere. The full decoded question
	// below must then have exactly the client's case. A suffix that would require
	// changing shared bytes outside this prefix is conservatively rejected.
	for off, expanded := 12, 0; response[off] != 0 && response[off]&0xc0 == 0; {
		length := int(response[off])
		copy(candidate[off+1:off+1+length], want.Name.Wire[expanded+1:expanded+1+length])
		off += 1 + length
		expanded += 1 + length
	}
	var s Scanner
	if err := s.Init(candidate); err != nil {
		return 0, err
	}
	if s.Question.Name.Length != want.Name.Length || s.Question.Name.Wire != want.Name.Wire {
		return 0, ErrNameChanged
	}
	s.reference = response
	var rr Record
	for s.Next(&rr) {
	}
	if s.Err() != nil {
		return 0, s.Err()
	}
	return copy(dst, candidate), nil
}
