package dnswire

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
			require.LessOrEqual(t, q.End, len(p), "question end")
			require.Equal(t, 12, q.Start, "question start")
			require.Greater(t, q.End, q.Start, "question bounds")
			assert.Equal(t, p[q.Start:q.End], q.Original)
			checkNameOracle(t, p, 12, &q.Name)
			var canonical Name
			require.NoError(t, DecodeName(q.Name.Canonical[:q.Name.Length], 0, &canonical))
			assert.Equal(t, q.Name.Canonical[:q.Name.Length], canonical.Canonical[:canonical.Length], "canonical idempotence")
		}
		assert.Equal(t, before, p, "input mutated")
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
			require.GreaterOrEqual(t, n.Length, uint16(1))
			require.LessOrEqual(t, n.Length, uint16(255))
			require.Greater(t, n.End, off)
			require.LessOrEqual(t, n.End, len(p))
			checkNameOracle(t, p, off, &n)
		}
		assert.Equal(t, before, p, "input mutated")
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
				require.Equal(t, off, r.Start)
				require.Greater(t, r.End, off)
				require.LessOrEqual(t, r.End, len(p))
				require.GreaterOrEqual(t, r.DataOffset, 0)
				require.LessOrEqual(t, r.DataOffset, r.End)
				// bytes.Equal intentionally treats nil and empty RDATA alike.
				assert.True(t, bytes.Equal(r.RData, p[r.DataOffset:r.End]), "borrowed RDATA")
				require.LessOrEqual(t, count, len(p)/11)
				off = r.End
			}
			if s.Err() == nil {
				assert.Equal(t, len(p), off, "trailing data")
			}
		}
		var m Message
		fullErr := ScanMessage(p, &m)
		assert.Equal(t, err == nil && s.Err() == nil, fullErr == nil, "scanner/whole-message mismatch")
		_ = ParseRequest(p, &m)
		assert.Equal(t, before, p, "input mutated")
	})
}

func TestCommonPathAllocations(t *testing.T) {
	p := query()
	var m Message
	n := testing.AllocsPerRun(1000, func() {
		if err := ParseRequest(p, &m); err != nil {
			panic(err)
		}
	})
	assert.Zero(t, n, "request allocations")
	p = answer(1, fromHex("c0000201"))
	n = testing.AllocsPerRun(1000, func() {
		if err := ScanMessage(p, &m); err != nil {
			panic(err)
		}
	})
	assert.Zero(t, n, "scan allocations")
}

func BenchmarkQuestion(b *testing.B) {
	p := query()
	var q Question
	var err error
	b.ReportAllocs()
	for b.Loop() {
		if err = ParseQuestion(p, &q); err != nil {
			break
		}
	}
	require.NoError(b, err)
}
func BenchmarkRequest(b *testing.B) {
	p := query()
	var m Message
	var err error
	b.ReportAllocs()
	for b.Loop() {
		if err = ParseRequest(p, &m); err != nil {
			break
		}
	}
	require.NoError(b, err)
}
func BenchmarkResponse(b *testing.B) {
	p := answer(1, fromHex("c0000201"))
	var m Message
	var err error
	b.ReportAllocs()
	for b.Loop() {
		if err = ScanMessage(p, &m); err != nil {
			break
		}
	}
	require.NoError(b, err)
}
