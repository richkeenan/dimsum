// Package dnscache provides an optional fresh-answer cache. It is not a policy
// engine: callers authenticate upstream replies and re-inspect aliases on hits.
package dnscache

import (
	"bytes"
	"encoding/binary"
	"github.com/richkeenan/dimsum/internal/dnswire"
)

// Key owns its complete canonical identity; no request storage is retained.
// Namespace, policy generation and feature bits must describe the snapshot that
// initiated resolution, including when an old resolution completes after reload.
type Key struct {
	data   [288]byte
	length uint16
}

// NewKey consumes a successfully ParseRequest-validated message. Option-bearing
// requests and non-query sections bypass caching. EDNS presence and UDP size are
// isolated too: this library does not rebuild OPT or truncate replies.
func NewKey(q *dnswire.Message, namespace, generation, features uint64) (Key, bool) {
	var k Key
	if q.Question.RequestError() != nil || q.HasAuthentication || q.Question.Name.Length == 0 || q.Question.Name.Length > dnswire.MaxNameLen || q.Question.Header.Answers != 0 || q.Question.Header.Authorities != 0 || len(q.EDNS.Options) != 0 || q.EDNS.Version != 0 || q.EDNS.ExtendedRCode != 0 || q.EDNS.Flags&^uint16(0x8000) != 0 {
		return k, false
	}
	additional := uint16(0)
	if q.EDNS.Present {
		additional = 1
	}
	if q.Question.Header.Additionals != additional {
		return k, false
	}
	binary.BigEndian.PutUint64(k.data[:], namespace)
	binary.BigEndian.PutUint64(k.data[8:], generation)
	binary.BigEndian.PutUint64(k.data[16:], features)
	binary.BigEndian.PutUint16(k.data[24:], q.Question.Type)
	binary.BigEndian.PutUint16(k.data[26:], q.Question.Class)
	if q.EDNS.Present {
		k.data[28] |= 1
		binary.BigEndian.PutUint16(k.data[29:], q.EDNS.UDPSize)
	}
	if q.EDNS.DO {
		k.data[28] |= 2
	}
	if q.Question.Header.Flags&dnswire.FlagCD != 0 {
		k.data[28] |= 4
	}
	k.length = uint16(31 + int(q.Question.Name.Length))
	copy(k.data[31:], q.Question.Name.Canonical[:q.Question.Name.Length])
	return k, true
}

func (k *Key) valid() bool { return k.length >= 32 && int(k.length) <= len(k.data) }
func (k *Key) matches(q *dnswire.Message) bool {
	return k.valid() && binary.BigEndian.Uint16(k.data[24:]) == q.Question.Type && binary.BigEndian.Uint16(k.data[26:]) == q.Question.Class &&
		bytes.Equal(k.data[31:k.length], q.Question.Name.Canonical[:q.Question.Name.Length]) &&
		(k.data[28]&1 != 0) == q.EDNS.Present && (k.data[28]&2 != 0) == q.EDNS.DO && (k.data[28]&4 != 0) == (q.Question.Header.Flags&dnswire.FlagCD != 0)
}
