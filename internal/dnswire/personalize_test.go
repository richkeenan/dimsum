package dnswire_test

import (
	"bytes"
	"encoding/binary"
	"github.com/miekg/dns"
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
			if test.name == "owner" {
				var beforeOracle, afterOracle dns.Msg
				require.NoError(t, beforeOracle.Unpack(p))
				require.NoError(t, afterOracle.Unpack(naive))
				require.Equal(t, ".", beforeOracle.Answer[0].Header().Name)
				require.NotEqual(t, beforeOracle.Answer[0].Header().Name, afterOracle.Answer[0].Header().Name)
			}
			out := bytes.Repeat([]byte{0xcc}, 512)
			n, err := dnswire.PersonalizeReply(out, p, &request)
			assert.ErrorIs(t, err, dnswire.ErrNameChanged)
			assert.Zero(t, n)
			assert.Equal(t, bytes.Repeat([]byte{0xcc}, 512), out)
		})
	}
}

func TestPersonalizeRejectsFlagDependentName(t *testing.T) {
	wire := []byte{0x12, 1, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 6, 'A', 'B', 'C', 'D', 'E', 'F', 0, 0, 1, 0, 1}
	var request, original, naive dnswire.Message
	require.NoError(t, dnswire.ParseRequest(wire, &request))
	p := bytes.Clone(wire)
	p[2], p[3], p[7] = 0x81, 0x20, 1 // AD=1; pointer sees a 32-byte label.
	p = appendPersonalizationRR(p, []byte{0xc0, 3}, 1, []byte{0, 0, 0, 0})
	require.NoError(t, dnswire.ScanMessage(p, &original))
	changed := bytes.Clone(p)
	changed[3] = 0 // AD=0; same pointer now sees root, still structurally valid.
	require.NoError(t, dnswire.ScanMessage(changed, &naive))
	_, err := dnswire.PersonalizeReply(make([]byte, 512), p, &request)
	assert.ErrorIs(t, err, dnswire.ErrNameChanged)
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

func TestPersonalizeCompressedQuestionWithOpaqueRecords(t *testing.T) {
	request := personalizationRequest(t)
	p := personalizationResponse()
	// Replace the root terminator with a backward pointer to the immutable
	// QDCOUNT high byte. This is acyclic and actually expands to a., unlike
	// an a.a fixture that points back to its own first label and cycles.
	p = append(append(bytes.Clone(p[:14]), 0xc0, 4), p[15:]...)
	p[7] = 1
	p = appendPersonalizationRR(p, []byte{0xc0, 12}, 65400, []byte{0xc0, 1, 0xff, 0})
	var oracle dns.Msg
	require.NoError(t, oracle.Unpack(p))
	require.Equal(t, "a.", oracle.Question[0].Name)
	var m dnswire.Message
	require.NoError(t, dnswire.ScanMessage(p, &m))
	require.True(t, m.Question.Name.Compressed)
	before := bytes.Clone(p)
	n, err := dnswire.PersonalizeReply(p, p, &request)
	require.NoError(t, err)
	assert.Equal(t, len(before), n)
	assert.Equal(t, before[14:], p[14:], "compressed pointer, counts and RR offsets must remain intact")
	require.NoError(t, oracle.Unpack(p))
	assert.Equal(t, "A.", oracle.Question[0].Name)
	assert.Equal(t, "A.", oracle.Answer[0].Header().Name)
	assert.Equal(t, "c001ff00", oracle.Answer[0].(*dns.RFC3597).Rdata)
}

func TestPersonalizeRebuildsOnlyRecordFreeCompressedQuestion(t *testing.T) {
	// Original ID low byte is a root terminator. The client's low byte is
	// different, so retaining this pointer would change the question itself.
	p := []byte{0x12, 0, 0x81, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0xc0, 1, 0, 1, 0, 1}
	query := []byte{0x12, 1, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 1, 0, 1}
	var request, response dnswire.Message
	require.NoError(t, dnswire.ParseRequest(query, &request))
	require.NoError(t, dnswire.ScanMessage(p, &response))
	var oracle dns.Msg
	require.NoError(t, oracle.Unpack(p))
	require.Equal(t, ".", oracle.Question[0].Name)
	out := make([]byte, 512)
	n, err := dnswire.PersonalizeReply(out, p, &request)
	require.NoError(t, err)
	require.NoError(t, oracle.Unpack(out[:n]))
	assert.EqualValues(t, 0x1201, oracle.Id)
	assert.Equal(t, ".", oracle.Question[0].Name)
	assert.Equal(t, len(query), n)
	// Records make relocation unsafe, including opaque pointer-shaped data.
	p[7] = 1
	p = appendPersonalizationRR(p, []byte{0}, 65400, []byte{0xc0, 12})
	require.NoError(t, dnswire.ScanMessage(p, &response))
	before := bytes.Clone(p)
	n, err = dnswire.PersonalizeReply(p, p, &request)
	assert.ErrorIs(t, err, dnswire.ErrNameChanged)
	assert.Zero(t, n)
	assert.Equal(t, before, p, "failed aliasing personalization mutated original")
}

func TestPersonalizeRejectsCyclicQuestionFixture(t *testing.T) {
	p := personalizationResponse()
	// a + pointer to the start of that same a + pointer ... is NOT a.a.
	p = append(append(bytes.Clone(p[:14]), 0xc0, 12), p[15:]...)
	var m dnswire.Message
	assert.Error(t, dnswire.ScanMessage(p, &m))
	var oracle dns.Msg
	assert.Error(t, oracle.Unpack(p))
	request := personalizationRequest(t)
	_, err := dnswire.PersonalizeReply(make([]byte, 512), p, &request)
	assert.Error(t, err)
}
