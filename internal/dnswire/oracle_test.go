package dnswire

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/miekg/dns"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/dns/dnsmessage"
)

func TestIndependentOracles(t *testing.T) {
	text, err := os.ReadFile("testdata/dns/rfc1035-a.hex")
	require.NoError(t, err)
	fixture := fromHex(strings.TrimSpace(string(text)))
	if !bytes.Equal(fixture, answer(1, fromHex("c0000201"))) { // Fixture RA bit is independent of helper.
		p := answer(1, fromHex("c0000201"))
		p[3] = 0x80
		require.Equal(t, fixture, p, "fixture/helper disagreement")
	}
	inputs := [][]byte{fixture, query()}
	for _, tt := range recordFixtures {
		inputs = append(inputs, answer(tt.typ, fromHex(tt.data)))
	}
	p := query()
	p[11] = 1
	p = append(p, opt(1232, 0x8000, fromHex("000c00020000"))...)
	inputs = append(inputs, p)
	for i, p := range inputs {
		var ours Message
		require.NoError(t, ScanMessage(p, &ours), "fixture %d", i)
		var m dns.Msg
		require.NoError(t, m.Unpack(p), "miekg fixture %d", i)
		var independent dnsmessage.Message
		require.NoError(t, independent.Unpack(p), "x/net fixture %d", i)
		assert.Equal(t, ours.Question.Header.ID, m.Id, "fixture %d", i)
		require.Len(t, m.Question, 1, "fixture %d", i)
		require.Len(t, independent.Questions, 1, "fixture %d", i)
		assert.Equal(t, "WWW.Example.COM.", m.Question[0].Name, "fixture %d", i)
		assert.Equal(t, ours.Question.Type, m.Question[0].Qtype, "fixture %d", i)
		assert.Equal(t, ours.Question.Class, m.Question[0].Qclass, "fixture %d", i)
		assert.Equal(t, m.Question[0].Name, independent.Questions[0].Name.String(), "fixture %d", i)
		assert.Len(t, m.Answer, int(ours.Question.Header.Answers), "fixture %d", i)
		require.Len(t, independent.Answers, len(m.Answer), "fixture %d", i)
		assert.Len(t, m.Extra, int(ours.Question.Header.Additionals), "fixture %d", i)
		if len(m.Answer) > 0 {
			h := m.Answer[0].Header()
			ih := independent.Answers[0].Header
			var s Scanner
			require.NoError(t, s.Init(p))
			var r Record
			require.True(t, s.Next(&r), "fixture %d: %v", i, s.Err())
			assert.Equal(t, "WWW.Example.COM.", h.Name, "fixture %d", i)
			assert.Equal(t, r.Type, h.Rrtype, "fixture %d", i)
			assert.Equal(t, r.TTL, h.Ttl, "fixture %d", i)
			assert.Equal(t, r.Type, uint16(ih.Type), "fixture %d", i)
			assert.Equal(t, r.TTL, ih.TTL, "fixture %d", i)
		}
	}
}

func checkNameOracle(t testing.TB, p []byte, off int, n *Name) {
	t.Helper()
	display, end, err := dns.UnpackDomainName(p, off)
	require.NoError(t, err)
	assert.Equal(t, n.End, end, "oracle end")
	var wire [255]byte
	next, err := dns.PackDomainName(display, wire[:], 0, nil, false)
	require.NoError(t, err)
	assert.Equal(t, n.Wire[:n.Length], wire[:next], "oracle roundtrip %q", display)
}

func TestBinaryAndUnknownOracle(t *testing.T) {
	for _, p := range [][]byte{{0}, {4, 'A', '.', 0xff, 0, 2, '_', 'B', 0}, {1, 'a', 0, 0xc0, 0, 0xc0, 3}} {
		off := 0
		if len(p) == 7 {
			off = 5
		}
		var n Name
		require.NoError(t, DecodeName(p, off, &n))
		checkNameOracle(t, p, off, &n)
	}
	p := query()
	p[29], p[30] = 0xff, 0x78
	var q Question
	require.NoError(t, ParseQuestion(p, &q))
	var m dns.Msg
	require.NoError(t, m.Unpack(p))
	require.Len(t, m.Question, 1)
	assert.Equal(t, uint16(65400), q.Type, "unknown QTYPE lost")
	assert.Equal(t, uint16(65400), m.Question[0].Qtype, "unknown QTYPE lost")
	var unknown dns.Msg
	require.NoError(t, unknown.Unpack(answer(65400, fromHex("c0ff00ff"))))
	require.Len(t, unknown.Answer, 1)
	rr, ok := unknown.Answer[0].(*dns.RFC3597)
	require.True(t, ok, "unknown RR should use RFC3597 representation")
	assert.Equal(t, "c0ff00ff", rr.Rdata, "unknown bytes interpreted as pointers")
}
