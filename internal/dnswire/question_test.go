package dnswire

import (
	"bytes"
	"encoding/hex"
	"errors"
	"testing"
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
	if err := ParseQuestion(p, &q); err != nil {
		t.Fatal(err)
	}
	if q.Header.ID != 0x1234 || q.Header.Flags != 0xffff || q.Type != 1 || q.Class != 1 || q.Start != 12 || q.End != 33 || q.Name.End != 29 {
		t.Fatalf("question: %+v", q)
	}
	if !bytes.Equal(q.Name.Canonical[:q.Name.Length], []byte("\x03www\x07example\x03com\x00")) || !bytes.Equal(q.Original, p[12:33]) {
		t.Fatal("question bytes")
	}
	if !bytes.Equal(p, before) {
		t.Fatal("mutated input")
	}
	p[13] = 'X'
	if q.Original[1] != 'X' || q.Name.Wire[1] != 'W' {
		t.Fatal("borrowed/copy contract")
	}
}

func TestQuestionValidation(t *testing.T) {
	for n := 0; n < len(query()); n++ {
		var q Question
		if err := ParseQuestion(query()[:n], &q); err == nil {
			t.Fatalf("accepted prefix %d", n)
		}
	}
	for _, count := range []byte{0, 2, 255} {
		p := query()
		p[5] = count
		var q Question
		if err := ParseQuestion(p, &q); !errors.Is(err, ErrQuestionCount) {
			t.Fatalf("count %d: %v", count, err)
		}
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
			if err := ParseQuestion(p, &q); err != nil {
				t.Fatal(err)
			}
			if err := q.RequestError(); !errors.Is(err, tt.want) {
				t.Fatalf("%v want %v", err, tt.want)
			}
		})
	}
	var q Question
	if err := ParseQuestion(make([]byte, 65536), &q); !errors.Is(err, ErrBounds) {
		t.Fatal(err)
	}
}

func TestCompressedQuestion(t *testing.T) {
	// Prior name at ID bytes is structurally decodable; callers must rebuild
	// the copied Name.Wire rather than relocate this borrowed pointer.
	p := fromHex("014100000001000000000000c000ffff0001")
	var q Question
	if err := ParseQuestion(p, &q); err != nil {
		t.Fatal(err)
	}
	if q.End != 18 || !q.Name.Compressed || !bytes.Equal(q.Name.Canonical[:q.Name.Length], []byte{1, 'a', 0}) || !bytes.Equal(q.Original, fromHex("c000ffff0001")) {
		t.Fatal(q)
	}
}
