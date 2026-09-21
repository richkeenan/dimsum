package dnswire

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fromHex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

// Independently encoded RFC 1035 question: ID 0x1234, RD, WWW.Example.COM IN A.
func query() []byte {
	return fromHex("12340100000100000000000003575757074578616d706c6503434f4d0000010001")
}

func TestQuestionOffsetsFlagsAndOwnership(t *testing.T) {
	p := query()
	p[2], p[3] = 0xff, 0xff
	before := bytes.Clone(p)
	var q Question
	require.NoError(t, ParseQuestion(p, &q))
	assert.Equal(t, uint16(0x1234), q.Header.ID)
	assert.Equal(t, uint16(0xffff), q.Header.Flags)
	assert.Equal(t, uint16(1), q.Type)
	assert.Equal(t, uint16(1), q.Class)
	assert.Equal(t, 12, q.Start)
	assert.Equal(t, 33, q.End)
	assert.Equal(t, 29, q.Name.End)
	assert.Equal(t, []byte("\x03www\x07example\x03com\x00"), q.Name.Canonical[:q.Name.Length])
	assert.Equal(t, p[12:33], q.Original)
	assert.Equal(t, before, p, "mutated input")
	p[13] = 'X'
	require.Greater(t, len(q.Original), 1)
	assert.Equal(t, byte('X'), q.Original[1], "borrowed question contract")
	assert.Equal(t, byte('W'), q.Name.Wire[1], "copied name contract")
}

func TestQuestionValidation(t *testing.T) {
	for n := 0; n < len(query()); n++ {
		var q Question
		assert.Error(t, ParseQuestion(query()[:n], &q), "accepted prefix %d", n)
	}
	for _, count := range []byte{0, 2, 255} {
		p := query()
		p[5] = count
		var q Question
		assert.ErrorIs(t, ParseQuestion(p, &q), ErrQuestionCount, "count %d", count)
	}
	tests := []struct {
		name              string
		flags, typ, class uint16
		want              error
	}{
		{"ordinary", 0x0100, 1, 1, nil}, {"rd0", 0, 1, 1, nil}, {"cd-ad", 0x0130, 1, 1, nil},
		{"unknown-type", 0, 65400, 1, nil}, {"response", 0x8100, 1, 1, ErrResponse},
		{"opcode", 0x0800, 1, 1, ErrOpcode}, {"update", 0x2800, 1, 1, ErrOpcode},
		{"class", 0x0100, 1, 3, ErrClass}, {"axfr", 0, 252, 1, ErrUnsupported},
		{"ixfr", 0, 251, 1, ErrUnsupported}, {"tkey", 0, 249, 1, ErrUnsupported}, {"tsig", 0, 250, 1, ErrUnsupported},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := query()
			p[2], p[3] = byte(tt.flags>>8), byte(tt.flags)
			p[29], p[30] = byte(tt.typ>>8), byte(tt.typ)
			p[31], p[32] = byte(tt.class>>8), byte(tt.class)
			var q Question
			require.NoError(t, ParseQuestion(p, &q))
			assert.ErrorIs(t, q.RequestError(), tt.want)
			var m Message
			assert.ErrorIs(t, ParseRequest(p, &m), tt.want, "whole request")
		})
	}
	var q Question
	assert.ErrorIs(t, ParseQuestion(make([]byte, 65536), &q), ErrBounds)
}

func TestCompressedQuestion(t *testing.T) {
	// Prior name at ID bytes is structurally decodable; callers must rebuild
	// the copied Name.Wire rather than relocate this borrowed pointer.
	p := fromHex("014100000001000000000000c000ffff0001")
	var q Question
	require.NoError(t, ParseQuestion(p, &q))
	assert.Equal(t, 18, q.End)
	assert.True(t, q.Name.Compressed)
	assert.Equal(t, []byte{1, 'a', 0}, q.Name.Canonical[:q.Name.Length])
	assert.Equal(t, fromHex("c000ffff0001"), q.Original)
}
