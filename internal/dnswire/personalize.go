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
// responsibility. The temporary candidate is owned only for this call.
func PersonalizeReply(dst, response []byte, request *Message) (int, error) {
	var original Message
	if err := ScanMessage(response, &original); err != nil {
		return 0, err
	}
	q, want := &original.Question, &request.Question
	if q.Header.Flags&FlagQR == 0 || q.Type != want.Type || q.Class != want.Class || q.Name.Length != want.Name.Length || q.Name.Canonical != want.Name.Canonical {
		return 0, ErrNameChanged
	}
	if q.Name.Compressed {
		return 0, ErrNameChanged
	}
	if len(dst) < len(response) {
		return 0, ErrBounds
	}
	candidate := bytes.Clone(response)
	binary.BigEndian.PutUint16(candidate, want.Header.ID)
	flags := q.Header.Flags &^ (FlagAD | FlagRD | FlagCD)
	flags |= want.Header.Flags & (FlagRD | FlagCD)
	binary.BigEndian.PutUint16(candidate[2:4], flags)
	copy(candidate[12:12+int(want.Name.Length)], want.Name.Wire[:want.Name.Length])
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
