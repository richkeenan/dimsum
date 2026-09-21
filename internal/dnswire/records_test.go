package dnswire

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

// RR owner is a pointer to offset 12; the independent fixture RDATA is opaque
// to this test helper. RR starts at 33, TTL at 39, RDATA at 45.
func answer(typ uint16, data []byte) []byte {
	p := query()
	p[2] = 0x81
	p[7] = 1
	p = append(p, 0xc0, 12, byte(typ>>8), byte(typ), 0, 1, 0, 0, 1, 44, byte(len(data)>>8), byte(len(data)))
	return append(p, data...)
}

var recordFixtures = []struct {
	name string
	typ  uint16
	data string
}{
	{"A", 1, "c0000201"}, {"AAAA", 28, "20010db8000000000000000000000001"},
	{"NS", 2, "c00c"}, {"CNAME", 5, "c00c"}, {"DNAME", 39, "016100"}, {"PTR", 12, "c00c"},
	{"SOA", 6, "c00c0161000000000100000002000000030000000400000005"},
	{"TXT", 16, "036100ff00"}, {"MX", 15, "000ac00c"}, {"SRV", 33, "0001000201bb016100"},
	{"HTTPS", 65, "0000016100"}, {"SVCB", 64, "00010000010003026832"},
	{"DNSKEY", 48, "0101030801020304"}, {"DS", 43, "0001080201020304"},
	{"RRSIG", 46, "000108020000012c000000020000000100010161000102"},
	{"NSEC", 47, "016100000140"}, {"unknown", 65400, "c0ff00ff"},
}

func TestRecordMatrix(t *testing.T) {
	for _, tt := range recordFixtures {
		t.Run(tt.name, func(t *testing.T) {
			p := answer(tt.typ, fromHex(tt.data))
			before := bytes.Clone(p)
			var s Scanner
			if err := s.Init(p); err != nil {
				t.Fatal(err)
			}
			var rr Record
			if !s.Next(&rr) {
				t.Fatal(s.Err())
			}
			if rr.Type != tt.typ || rr.Class != 1 || rr.TTL != 300 || rr.TTLOffset != 39 || rr.Start != 33 || rr.DataOffset != 45 || rr.End != len(p) || rr.Section != Answer || !bytes.Equal(rr.RData, fromHex(tt.data)) {
				t.Fatalf("record %+v", rr)
			}
			if s.Next(&rr) || s.Err() != nil {
				t.Fatalf("end: %v", s.Err())
			}
			if !bytes.Equal(p, before) {
				t.Fatal("input mutated")
			}
			p[45] ^= 0xff
			if rr.RData[0] != p[45] || rr.Name.Wire[1] != 'W' {
				t.Fatal("borrowed RDATA / copied owner contract")
			}
		})
	}
}

func opt(size uint16, ttl uint32, data []byte) []byte {
	r := []byte{0, 0, 41, byte(size >> 8), byte(size), 0, 0, 0, 0, byte(len(data) >> 8), byte(len(data))}
	binary.BigEndian.PutUint32(r[5:9], ttl)
	return append(r, data...)
}

func TestEDNS(t *testing.T) {
	for _, size := range []uint16{512, 1232, 4096} {
		p := query()
		p[11] = 1
		// ECS, COOKIE, padding, EDE and an unknown option; only TLV syntax is
		// interpreted here, not endpoint-specific option semantics.
		options := fromHex("0008000400010000000a00080102030405060708000c00020000000f00020000fde80000")
		p = append(p, opt(size, 0x00008000, options)...)
		var m Message
		if err := ParseRequest(p, &m); err != nil {
			t.Fatal(err)
		}
		if !m.EDNS.Present || !m.EDNS.DO || m.EDNS.UDPSize != size || m.EDNS.Version != 0 || m.EDNS.Flags != 0x8000 || m.EDNS.RecordOffset != 33 || !bytes.Equal(m.EDNS.Options, options) {
			t.Fatalf("EDNS %+v", m.EDNS)
		}
		codes := []uint16{8, 10, 12, 15, 65000}
		off := 0
		for _, code := range codes {
			o, next, err := ReadOption(m.EDNS.Options, off)
			if err != nil || o.Code != code || next <= off {
				t.Fatalf("option %+v %d %v", o, next, err)
			}
			off = next
		}
		if off != len(options) {
			t.Fatal(off)
		}
		var s Scanner
		if err := s.Init(p); err != nil {
			t.Fatal(err)
		}
		var rr Record
		if !s.Next(&rr) || rr.TTLOffset != -1 || rr.TTL != 0x8000 {
			t.Fatalf("OPT treated as TTL: %+v", rr)
		}
	}
	var m Message
	if err := ParseRequest(query(), &m); err != nil || m.EDNS.Present || m.RCode != 0 {
		t.Fatalf("plain %v %+v", err, m)
	}
	p := query()
	p[11] = 1
	p = append(p, opt(1232, 0x01000000, nil)...)
	p[2] = 0x81
	p[3] = 3
	if err := ScanMessage(p, &m); err != nil || m.RCode != 19 {
		t.Fatalf("extended rcode %d %v", m.RCode, err)
	}
	p[2] = 1
	p[3] = 0
	if err := ParseRequest(p, &m); !errors.Is(err, ErrEDNS) {
		t.Fatalf("request extended rcode: %v", err)
	}
	p = query()
	p[11] = 1
	p = append(p, opt(1232, 0x00010000, nil)...)
	if err := ParseRequest(p, &m); !errors.Is(err, ErrBadVersion) || m.EDNS.Version != 1 {
		t.Fatalf("BADVERS %v", err)
	}
	if err := ScanMessage(p, &m); err != nil {
		t.Fatalf("structurally valid unknown version: %v", err)
	}
}

func TestMalformedRecordsAndEDNS(t *testing.T) {
	for n := 33; n < len(answer(1, fromHex("c0000201"))); n++ {
		var m Message
		if err := ScanMessage(answer(1, fromHex("c0000201"))[:n], &m); err == nil {
			t.Fatalf("accepted prefix %d", n)
		}
	}
	for _, tt := range []struct {
		typ  uint16
		data string
	}{
		{1, "00"}, {28, "00"}, {2, "c0ff"}, {5, "00ff"}, {39, "c00c"}, {12, ""},
		{6, "000000"}, {6, "0000000000010000000200000003000000040000000500"},
		{16, "0361"}, {15, "000a"}, {33, "0001000201bbc00c"},
		{64, "0000000001"}, {65, "0000c00c"}, {64, "0001000002000000010000"},
		{46, "000108"}, {47, "0161000000"},
	} {
		var m Message
		if err := ScanMessage(answer(tt.typ, fromHex(tt.data)), &m); err == nil {
			t.Fatalf("accepted malformed type %d data %s", tt.typ, tt.data)
		}
	}
	for _, data := range []string{"00", "000800", "00080002ff"} {
		p := query()
		p[11] = 1
		p = append(p, opt(1232, 0, fromHex(data))...)
		var m Message
		if err := ScanMessage(p, &m); !errors.Is(err, ErrEDNS) {
			t.Fatalf("TLV %s: %v", data, err)
		}
	}
	tests := []struct {
		name   string
		modify func([]byte) []byte
		want   error
	}{
		{"trailing", func(p []byte) []byte { return append(p, 0) }, ErrTrailing},
		{"duplicate-opt", func(p []byte) []byte { p[11] = 2; return append(append(p, opt(1232, 0, nil)...), opt(512, 0, nil)...) }, ErrEDNS},
		{"answer-opt", func(p []byte) []byte { p[7] = 1; return append(p, opt(1232, 0, nil)...) }, ErrEDNS},
		{"nonroot-opt", func(p []byte) []byte {
			p[11] = 1
			r := opt(1232, 0, nil)
			return append(p, append([]byte{1, 'a'}, r...)...)
		}, ErrEDNS},
		{"missing-record", func(p []byte) []byte { p[9] = 1; return p }, ErrBounds},
		{"invalid-owner", func(p []byte) []byte { p = answer(1, fromHex("c0000201")); p[33] = 0x80; return p }, ErrLabel},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var m Message
			if err := ScanMessage(tt.modify(query()), &m); !errors.Is(err, tt.want) {
				t.Fatalf("%v want %v", err, tt.want)
			}
		})
	}
	for _, off := range []int{-1, 0, 1, 99} {
		if _, _, err := ReadOption(nil, off); !errors.Is(err, ErrEDNS) {
			t.Fatal(err)
		}
	}
}

func TestSectionsAndScannerFailure(t *testing.T) {
	p := answer(1, fromHex("c0000201"))
	r := bytes.Clone(p[33:])
	p[9], p[11] = 1, 1
	p = append(p, r...)
	p = append(p, r...)
	var s Scanner
	if err := s.Init(p); err != nil {
		t.Fatal(err)
	}
	var rr Record
	for _, section := range []Section{Answer, Authority, Additional} {
		if !s.Next(&rr) || rr.Section != section {
			t.Fatalf("section %d: %v", section, s.Err())
		}
	}
	if s.Next(&rr) || s.Err() != nil {
		t.Fatal(s.Err())
	}
	p = append(p, 0)
	if err := s.Init(p); err != nil {
		t.Fatal(err)
	}
	for s.Next(&rr) {
	}
	if !errors.Is(s.Err(), ErrTrailing) || s.Next(&rr) {
		t.Fatal(s.Err())
	}
	if err := s.Init(nil); !errors.Is(err, ErrBounds) || s.Next(&rr) {
		t.Fatal("failed init usable")
	}
	var m Message
	p = answer(250, nil)
	p[2] = 1
	if err := ParseRequest(p, &m); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("TSIG RR: %v", err)
	}
}
