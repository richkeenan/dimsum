package dnswire

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
			require.NoError(t, s.Init(p))
			var rr Record
			require.True(t, s.Next(&rr), "record missing: %v", s.Err())
			assert.Equal(t, tt.typ, rr.Type)
			assert.Equal(t, uint16(1), rr.Class)
			assert.Equal(t, uint32(300), rr.TTL)
			assert.Equal(t, 39, rr.TTLOffset)
			assert.Equal(t, 33, rr.Start)
			assert.Equal(t, 45, rr.DataOffset)
			assert.Equal(t, len(p), rr.End)
			assert.Equal(t, Answer, rr.Section)
			assert.Equal(t, fromHex(tt.data), rr.RData)
			require.False(t, s.Next(&rr), "unexpected extra record")
			assert.NoError(t, s.Err())
			assert.Equal(t, before, p, "input mutated")
			p[45] ^= 0xff
			require.NotEmpty(t, rr.RData)
			assert.Equal(t, p[45], rr.RData[0], "borrowed RDATA contract")
			assert.Equal(t, byte('W'), rr.Name.Wire[1], "copied owner contract")
		})
	}
}

func opt(size uint16, ttl uint32, data []byte) []byte {
	r := []byte{0, 0, 41, byte(size >> 8), byte(size), 0, 0, 0, 0, byte(len(data) >> 8), byte(len(data))}
	binary.BigEndian.PutUint32(r[5:9], ttl)
	return append(r, data...)
}

func TestEDNS(t *testing.T) {
	for _, size := range []uint16{0, 511, 512, 1232, 4096} {
		p := query()
		p[11] = 1
		// ECS, COOKIE, padding, EDE and an unknown option; only TLV syntax is
		// interpreted here, not endpoint-specific option semantics.
		options := fromHex("0008000400010000000a00080102030405060708000c00020000000f00020000fde80000")
		p = append(p, opt(size, 0x00008000, options)...)
		var m Message
		require.NoError(t, ParseRequest(p, &m))
		assert.True(t, m.EDNS.Present)
		assert.True(t, m.EDNS.DO)
		assert.Equal(t, size, m.EDNS.UDPSize)
		assert.Zero(t, m.EDNS.Version)
		assert.Equal(t, uint16(0x8000), m.EDNS.Flags)
		assert.Equal(t, 33, m.EDNS.RecordOffset)
		assert.Equal(t, options, m.EDNS.Options)
		codes := []uint16{8, 10, 12, 15, 65000}
		off := 0
		for _, code := range codes {
			o, next, err := ReadOption(m.EDNS.Options, off)
			require.NoError(t, err)
			assert.Equal(t, code, o.Code)
			require.Greater(t, next, off)
			off = next
		}
		assert.Equal(t, len(options), off)
		var s Scanner
		require.NoError(t, s.Init(p))
		var rr Record
		require.True(t, s.Next(&rr), "OPT missing: %v", s.Err())
		assert.Equal(t, -1, rr.TTLOffset, "OPT treated as TTL")
		assert.Equal(t, uint32(0x8000), rr.TTL)
	}
	var m Message
	require.NoError(t, ParseRequest(query(), &m))
	assert.False(t, m.EDNS.Present)
	assert.Zero(t, m.RCode)
	p := query()
	p[11] = 1
	p = append(p, opt(1232, 0x01000000, nil)...)
	p[2] = 0x81
	p[3] = 3
	require.NoError(t, ScanMessage(p, &m))
	assert.Equal(t, uint16(19), m.RCode, "extended rcode")
	p[2] = 1
	p[3] = 0
	assert.ErrorIs(t, ParseRequest(p, &m), ErrEDNS, "request extended rcode")
	p = query()
	p[11] = 1
	p = append(p, opt(1232, 0x00010000, nil)...)
	assert.ErrorIs(t, ParseRequest(p, &m), ErrBadVersion)
	assert.Equal(t, uint8(1), m.EDNS.Version)
	assert.NoError(t, ScanMessage(p, &m), "structurally valid unknown version")
}

func TestMalformedRecordsAndEDNS(t *testing.T) {
	for n := 33; n < len(answer(1, fromHex("c0000201"))); n++ {
		var m Message
		assert.Error(t, ScanMessage(answer(1, fromHex("c0000201"))[:n], &m), "accepted prefix %d", n)
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
		assert.Error(t, ScanMessage(answer(tt.typ, fromHex(tt.data)), &m), "accepted malformed type %d data %s", tt.typ, tt.data)
	}
	for _, data := range []string{"00", "000800", "00080002ff"} {
		p := query()
		p[11] = 1
		p = append(p, opt(1232, 0, fromHex(data))...)
		var m Message
		assert.ErrorIs(t, ScanMessage(p, &m), ErrEDNS, "TLV %s", data)
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
			assert.ErrorIs(t, ScanMessage(tt.modify(query()), &m), tt.want)
		})
	}
	for _, off := range []int{-1, 0, 1, 99} {
		_, _, err := ReadOption(nil, off)
		assert.ErrorIs(t, err, ErrEDNS, "offset %d", off)
	}
}

func TestSectionsAndScannerFailure(t *testing.T) {
	p := answer(1, fromHex("c0000201"))
	r := bytes.Clone(p[33:])
	p[9], p[11] = 1, 1
	p = append(p, r...)
	p = append(p, r...)
	var s Scanner
	require.NoError(t, s.Init(p))
	var rr Record
	for _, section := range []Section{Answer, Authority, Additional} {
		require.True(t, s.Next(&rr), "section %d: %v", section, s.Err())
		assert.Equal(t, section, rr.Section)
	}
	assert.False(t, s.Next(&rr))
	assert.NoError(t, s.Err())
	p = append(p, 0)
	require.NoError(t, s.Init(p))
	for s.Next(&rr) {
	}
	assert.ErrorIs(t, s.Err(), ErrTrailing)
	assert.False(t, s.Next(&rr))
	assert.ErrorIs(t, s.Init(nil), ErrBounds)
	assert.False(t, s.Next(&rr), "failed init usable")
	var m Message
	p = answer(250, nil)
	p[2] = 1
	assert.ErrorIs(t, ParseRequest(p, &m), ErrUnsupported, "TSIG RR")
}
