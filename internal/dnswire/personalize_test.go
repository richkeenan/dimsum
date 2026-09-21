package dnswire_test

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/richkeenan/dimsum/internal/dnswire"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func personalizationRequest(t *testing.T) dnswire.Message {
	t.Helper()
	wire := []byte{0x12, 1, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 1, 'A', 0, 0, 1, 0, 1}
	var m dnswire.Message
	require.NoError(t, dnswire.ParseRequest(wire, &m))
	return m
}

func personalizationResponse() []byte {
	return []byte{0x12, 0, 0x81, 0, 0, 1, 0, 0, 0, 0, 0, 0, 1, 'a', 0, 0, 1, 0, 1}
}

func appendPersonalizationRR(p, owner []byte, typ uint16, data []byte) []byte {
	p = append(p, owner...)
	p = binary.BigEndian.AppendUint16(p, typ)
	p = append(p, 0, 1, 0, 0, 0, 60)
	p = binary.BigEndian.AppendUint16(p, uint16(len(data)))
	return append(p, data...)
}

func TestPersonalizeRejectsChangedNameSemantics(t *testing.T) {
	request := personalizationRequest(t)
	for _, test := range []struct {
		name        string
		typ         uint16
		owner, data []byte
	}{
		{"owner", 1, []byte{0xc0, 1}, []byte{1, 2, 3, 4}},
		{"NS", 2, []byte{0xc0, 12}, []byte{0xc0, 1}},
		{"CNAME", 5, []byte{0xc0, 12}, []byte{0xc0, 1}},
		{"PTR", 12, []byte{0xc0, 12}, []byte{0xc0, 1}},
		{"MX", 15, []byte{0xc0, 12}, []byte{0, 10, 0xc0, 1}},
		{"SOA-first", 6, []byte{0xc0, 12}, append([]byte{0xc0, 1, 0}, make([]byte, 20)...)},
		{"SOA-second", 6, []byte{0xc0, 12}, append([]byte{0, 0xc0, 1}, make([]byte, 20)...)},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := personalizationResponse()
			p[7] = 1
			p = appendPersonalizationRR(p, test.owner, test.typ, test.data)
			var original, changed dnswire.Message
			require.NoError(t, dnswire.ScanMessage(p, &original), "fixture must be valid before patch")
			naive := bytes.Clone(p)
			binary.BigEndian.PutUint16(naive, request.Question.Header.ID)
			require.NoError(t, dnswire.ScanMessage(naive, &changed), "exploit remains structurally valid")
			out := bytes.Repeat([]byte{0xcc}, 512)
			n, err := dnswire.PersonalizeReply(out, p, &request)
			assert.ErrorIs(t, err, dnswire.ErrNameChanged)
			assert.Zero(t, n)
			assert.Equal(t, bytes.Repeat([]byte{0xcc}, 512), out)
		})
	}
}

func TestPersonalizeChecksCompletePointerChains(t *testing.T) {
	request := personalizationRequest(t)
	for _, embedded := range []bool{false, true} {
		p := personalizationResponse()
		p[7] = 2
		// Opaque RDATA holds a pointer used by the second RR. The first
		// pointer target is outside the header; its final dependency is not.
		dataOffset := len(p) + 1 + 10
		p = appendPersonalizationRR(p, []byte{0}, 65400, []byte{0xc0, 1})
		ptr := []byte{0xc0, byte(dataOffset)}
		if embedded {
			p = appendPersonalizationRR(p, []byte{0xc0, 12}, 5, ptr)
		} else {
			p = appendPersonalizationRR(p, ptr, 1, []byte{1, 2, 3, 4})
		}
		var m dnswire.Message
		require.NoError(t, dnswire.ScanMessage(p, &m))
		_, err := dnswire.PersonalizeReply(make([]byte, 512), p, &request)
		assert.ErrorIs(t, err, dnswire.ErrNameChanged)
	}
}

func TestPersonalizePreservesSafeCompressionAndOpaqueOffsets(t *testing.T) {
	request := personalizationRequest(t)
	p := personalizationResponse()
	p[7] = 2
	p = appendPersonalizationRR(p, []byte{0xc0, 12}, 5, []byte{0xc0, 12})
	p = appendPersonalizationRR(p, []byte{0xc0, 12}, 65400, []byte{0xc0, 1, 0xff, 0})
	before := bytes.Clone(p)
	n, err := dnswire.PersonalizeReply(p, p, &request)
	require.NoError(t, err)
	assert.Equal(t, len(before), n)
	assert.Equal(t, before[19:], p[19:], "RR layout/opaque bytes moved")
	var scanner dnswire.Scanner
	require.NoError(t, scanner.Init(p))
	var rr dnswire.Record
	for scanner.Next(&rr) {
		assert.Equal(t, byte('A'), rr.Name.Wire[1])
	}
	require.NoError(t, scanner.Err())
}
