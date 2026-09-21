package dnswire

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/miekg/dns"
	"golang.org/x/net/dns/dnsmessage"
)

func TestIndependentOracles(t *testing.T) {
	text, err := os.ReadFile("testdata/dns/rfc1035-a.hex")
	if err != nil {
		t.Fatal(err)
	}
	fixture := fromHex(strings.TrimSpace(string(text)))
	if !bytes.Equal(fixture, answer(1, fromHex("c0000201"))) { // Fixture RA bit is independent of helper.
		p := answer(1, fromHex("c0000201"))
		p[3] = 0x80
		if !bytes.Equal(fixture, p) {
			t.Fatal("fixture/helper disagreement")
		}
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
		if err := ScanMessage(p, &ours); err != nil {
			t.Fatalf("fixture %d: %v", i, err)
		}
		var m dns.Msg
		if err := m.Unpack(p); err != nil {
			t.Fatalf("miekg fixture %d: %v", i, err)
		}
		var independent dnsmessage.Message
		if err := independent.Unpack(p); err != nil {
			t.Fatalf("x/net fixture %d: %v", i, err)
		}
		if m.Id != ours.Question.Header.ID || len(m.Question) != 1 || m.Question[0].Name != "WWW.Example.COM." || m.Question[0].Qtype != ours.Question.Type || m.Question[0].Qclass != ours.Question.Class || independent.Questions[0].Name.String() != m.Question[0].Name {
			t.Fatalf("question fixture %d differs", i)
		}
		if len(m.Answer) != int(ours.Question.Header.Answers) || len(independent.Answers) != len(m.Answer) || len(m.Extra) != int(ours.Question.Header.Additionals) {
			t.Fatalf("counts fixture %d differ", i)
		}
		if len(m.Answer) > 0 {
			h := m.Answer[0].Header()
			ih := independent.Answers[0].Header
			var s Scanner
			_ = s.Init(p)
			var r Record
			s.Next(&r)
			if h.Name != "WWW.Example.COM." || h.Rrtype != r.Type || h.Ttl != r.TTL || uint16(ih.Type) != r.Type || ih.TTL != r.TTL {
				t.Fatalf("RR fixture %d differs", i)
			}
		}
	}
}

func checkNameOracle(t testing.TB, p []byte, off int, n *Name) {
	t.Helper()
	display, end, err := dns.UnpackDomainName(p, off)
	if err != nil || end != n.End {
		t.Fatalf("oracle end=%d err=%v, ours=%d", end, err, n.End)
	}
	var wire [255]byte
	next, err := dns.PackDomainName(display, wire[:], 0, nil, false)
	if err != nil || !bytes.Equal(wire[:next], n.Wire[:n.Length]) {
		t.Fatalf("oracle roundtrip %q %x: %v", display, wire[:next], err)
	}
}

func TestBinaryAndUnknownOracle(t *testing.T) {
	for _, p := range [][]byte{{0}, {4, 'A', '.', 0xff, 0, 2, '_', 'B', 0}, {1, 'a', 0, 0xc0, 0, 0xc0, 3}} {
		off := 0
		if len(p) == 7 {
			off = 5
		}
		var n Name
		if err := DecodeName(p, off, &n); err != nil {
			t.Fatal(err)
		}
		checkNameOracle(t, p, off, &n)
	}
	p := query()
	p[29], p[30] = 0xff, 0x78
	var q Question
	if err := ParseQuestion(p, &q); err != nil {
		t.Fatal(err)
	}
	var m dns.Msg
	if err := m.Unpack(p); err != nil {
		t.Fatal(err)
	}
	if q.Type != 65400 || m.Question[0].Qtype != 65400 {
		t.Fatal("unknown QTYPE lost")
	}
	var unknown dns.Msg
	if err := unknown.Unpack(answer(65400, fromHex("c0ff00ff"))); err != nil {
		t.Fatal(err)
	}
	if rr, ok := unknown.Answer[0].(*dns.RFC3597); !ok || rr.Rdata != "c0ff00ff" {
		t.Fatal("unknown bytes interpreted as pointers")
	}
}
