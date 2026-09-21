package dnswire

import (
	"bytes"
	"testing"
)

func FuzzQuestion(f *testing.F) {
	f.Add(query())
	f.Add(fromHex("014100000001000000000000c000ffff0001"))
	f.Add(fromHex("00000100000100000000000004ff2e004100ffff0001"))
	f.Add([]byte{0xc0, 0})
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, p []byte) {
		before := bytes.Clone(p)
		var q Question
		if err := ParseQuestion(p, &q); err == nil {
			if q.End > len(p) || q.Start != 12 || q.End <= q.Start || !bytes.Equal(q.Original, p[q.Start:q.End]) {
				t.Fatal("question offsets")
			}
			checkNameOracle(t, p, 12, &q.Name)
			var canonical Name
			if err := DecodeName(q.Name.Canonical[:q.Name.Length], 0, &canonical); err != nil || !bytes.Equal(canonical.Canonical[:canonical.Length], q.Name.Canonical[:q.Name.Length]) {
				t.Fatal("canonical idempotence")
			}
		}
		if !bytes.Equal(before, p) {
			t.Fatal("input mutated")
		}
	})
}

func FuzzName(f *testing.F) {
	for _, p := range [][]byte{{0}, {1, 'A', 0}, {1, 'a', 0, 0xc0, 0}, {0xc0, 0}, {4, 'A', '.', 0xff, 0, 0}, {0x40}} {
		f.Add(p, 0)
		f.Add(p, len(p)-2)
	}
	f.Fuzz(func(t *testing.T, p []byte, off int) {
		before := bytes.Clone(p)
		var n Name
		if err := DecodeName(p, off, &n); err == nil {
			if n.Length < 1 || n.Length > 255 || n.End <= off || n.End > len(p) {
				t.Fatal("name bounds")
			}
			checkNameOracle(t, p, off, &n)
		}
		if !bytes.Equal(before, p) {
			t.Fatal("input mutated")
		}
	})
}

func FuzzScanner(f *testing.F) {
	f.Add(query())
	f.Add([]byte{})
	for _, tt := range recordFixtures {
		f.Add(answer(tt.typ, fromHex(tt.data)))
	}
	for _, data := range []string{"", "000c00020000", "00080002ff"} {
		p := query()
		p[11] = 1
		p = append(p, opt(1232, 0x8000, fromHex(data))...)
		f.Add(p)
	}
	f.Fuzz(func(t *testing.T, p []byte) {
		before := bytes.Clone(p)
		var s Scanner
		err := s.Init(p)
		if err == nil {
			off, count := s.Question.End, 0
			var r Record
			for s.Next(&r) {
				count++
				if r.Start != off || r.End <= off || r.End > len(p) || r.DataOffset > r.End || !bytes.Equal(r.RData, p[r.DataOffset:r.End]) || count > len(p)/11 {
					t.Fatal("record bounds")
				}
				off = r.End
			}
			if s.Err() == nil && off != len(p) {
				t.Fatal("trailing data")
			}
		}
		var m Message
		fullErr := ScanMessage(p, &m)
		if (fullErr == nil) != (err == nil && s.Err() == nil) {
			t.Fatal("scanner/whole-message mismatch")
		}
		_ = ParseRequest(p, &m)
		if !bytes.Equal(before, p) {
			t.Fatal("input mutated")
		}
	})
}

func TestCommonPathAllocations(t *testing.T) {
	p := query()
	var m Message
	if n := testing.AllocsPerRun(1000, func() {
		if err := ParseRequest(p, &m); err != nil {
			panic(err)
		}
	}); n != 0 {
		t.Fatalf("request allocations %g", n)
	}
	p = answer(1, fromHex("c0000201"))
	if n := testing.AllocsPerRun(1000, func() {
		if err := ScanMessage(p, &m); err != nil {
			panic(err)
		}
	}); n != 0 {
		t.Fatalf("scan allocations %g", n)
	}
}

func BenchmarkQuestion(b *testing.B) {
	p := query()
	var q Question
	b.ReportAllocs()
	for b.Loop() {
		if err := ParseQuestion(p, &q); err != nil {
			b.Fatal(err)
		}
	}
}
func BenchmarkRequest(b *testing.B) {
	p := query()
	var m Message
	b.ReportAllocs()
	for b.Loop() {
		if err := ParseRequest(p, &m); err != nil {
			b.Fatal(err)
		}
	}
}
func BenchmarkResponse(b *testing.B) {
	p := answer(1, fromHex("c0000201"))
	var m Message
	b.ReportAllocs()
	for b.Loop() {
		if err := ScanMessage(p, &m); err != nil {
			b.Fatal(err)
		}
	}
}
