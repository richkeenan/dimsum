package dnswire

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Hand-encoded RDATA is intentional: unicast DNS oracles do not compress SRV,
// DNAME or NSEC RDATA, even when their general compression option is enabled.
func multicastRecordPacket(kind uint16, data []byte) []byte {
	p := []byte{0, 0, 0x84, 0, 0, 1, 0, 1, 0, 0, 0, 0}
	p = append(p, []byte("\x07example\x05local\x00")...)
	p = append(p, 0, 1, 0, 1) // question, also compression target at offset 12
	p = append(p, 0xc0, 12)
	p = binary.BigEndian.AppendUint16(p, kind)
	p = append(p, 0x80, 1, 0, 0, 0, 120)
	p = binary.BigEndian.AppendUint16(p, uint16(len(data)))
	return append(p, data...)
}

func TestMulticastCompressedRDataKeepsUnicastRules(t *testing.T) {
	for _, tt := range []struct {
		name string
		kind uint16
		data []byte
	}{
		{"SRV", 33, []byte{0, 0, 0, 0, 0x1b, 0x58, 0xc0, 12}},
		{"DNAME", 39, []byte{0xc0, 12}},
		{"NSEC", 47, []byte{0xc0, 12, 0, 1, 0x40}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := multicastRecordPacket(tt.kind, tt.data)
			var s Scanner
			var r Record
			require.NoError(t, s.InitMulticast(p))
			assert.True(t, s.Next(&r), "valid mDNS record must be accepted")
			assert.False(t, s.Next(&r))
			assert.NoError(t, s.Err())
			require.NoError(t, s.Init(p))
			assert.False(t, s.Next(&r), "unicast must still reject prohibited compression")
			assert.ErrorIs(t, s.Err(), ErrRecord)
		})
	}
}

func TestMulticastNSECRDataCannotDiscardOtherRecords(t *testing.T) {
	// RFC 6762 section 6.1 requires ignoring unsupported NSEC data, not the
	// entire message. Envelope truncation remains an error.
	p := multicastRecordPacket(47, []byte{0xc0, 12, 0, 33})
	var s Scanner
	var r Record
	require.NoError(t, s.InitMulticast(p))
	assert.True(t, s.Next(&r))
	assert.False(t, s.Next(&r))
	assert.NoError(t, s.Err())
	require.NoError(t, s.InitMulticast(p[:len(p)-1]))
	assert.False(t, s.Next(&r))
	assert.ErrorIs(t, s.Err(), ErrBounds)
}

func TestMulticastRejectsMalformedServiceTargets(t *testing.T) {
	for _, data := range [][]byte{
		{0, 0, 0, 0, 0, 80, 0xff, 0xff},  // forward/out-of-bounds pointer
		{0, 0, 0, 0, 0, 80, 0xc0},        // truncated pointer
		{0, 0, 0, 0, 0, 80, 0xc0, 12, 0}, // trailing target data
	} {
		var s Scanner
		var r Record
		require.NoError(t, s.InitMulticast(multicastRecordPacket(33, data)))
		assert.False(t, s.Next(&r))
		assert.Error(t, s.Err())
	}
}
