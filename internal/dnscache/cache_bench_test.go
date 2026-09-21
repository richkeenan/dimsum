package dnscache

import (
	"encoding/binary"
	"fmt"
	"github.com/miekg/dns"
	"github.com/richkeenan/dimsum/internal/dnswire"
	"github.com/stretchr/testify/require"
	"net"
	"testing"
	"time"
	"unsafe"
)

var benchResult CacheResult
var benchInserted bool
var benchLength int

func BenchmarkCache(b *testing.B) {
	for _, shards := range []int{4, 8, 16} {
		b.Run(fmt.Sprintf("shards%d", shards), func(b *testing.B) {
			q, w := fixture(b, "example.org.")
			k, _ := NewKey(&q, 1, 1, 0)
			now := time.Now()
			c, err := New(Config{Bytes: 1 << 20, Shards: shards})
			require.NoError(b, err)
			require.True(b, c.Put(k, w, now))
			out := make([]byte, 512)
			b.Run("hot", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					benchResult = c.Get(k, &q, out, now)
				}
			})
			b.Run("replace", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					benchInserted = c.Put(k, w, now)
				}
			})
			b.Run("evict", func(b *testing.B) {
				var gen uint64
				b.ReportAllocs()
				for b.Loop() {
					gen++
					key, _ := NewKey(&q, 1, gen, 0)
					benchInserted = c.Put(key, w, now)
				}
				b.StopTimer()
				stats := c.Stats()
				entries, occupied, arena := 0, 0, 0
				for _, s := range stats.Shards[:stats.ShardCount] {
					entries += s.Entries
					occupied += s.OccupiedBytes
					arena += s.ArenaBytes
				}
				b.ReportMetric(float64(occupied)/float64(arena)*100, "arena-%")
				b.ReportMetric(float64(stats.RetainedBytes)/float64(entries), "retained-B/entry")
			})
		})
	}
	b.Run("cold-construction", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			c, err := New(Config{Bytes: 1 << 20, Shards: 4})
			if err != nil {
				b.Fatal(err)
			}
			benchLength = c.Stats().RetainedBytes
		}
	})
}

// compactA is a deliberately narrow test-only candidate: one uncompressed
// question, same-owner A answers, no other sections. Each record retains only
// its TTL and four address octets; the encoder rebuilds compressed RR envelopes.
// It cannot represent unknown/name-bearing RDATA and is NOT a second engine.
type compactRecord struct {
	ttl     uint32
	address [4]byte
}
type compactA struct {
	header   [12]byte
	question []byte
	records  []compactRecord
}

func (c *compactA) reconstruct(dst []byte, q *dnswire.Message, elapsed uint32) int {
	copy(dst, c.header[:])
	binary.BigEndian.PutUint16(dst, q.Question.Header.ID)
	flags := binary.BigEndian.Uint16(dst[2:])&^(dnswire.FlagAD|dnswire.FlagRD|dnswire.FlagCD) | q.Question.Header.Flags&(dnswire.FlagRD|dnswire.FlagCD)
	binary.BigEndian.PutUint16(dst[2:], flags)
	copy(dst[12:], c.question)
	copy(dst[12:], q.Question.Name.Wire[:q.Question.Name.Length])
	off := 12 + len(c.question)
	for _, r := range c.records {
		copy(dst[off:], []byte{0xc0, 12, 0, 1, 0, 1})
		binary.BigEndian.PutUint32(dst[off+6:], r.ttl-elapsed)
		binary.BigEndian.PutUint16(dst[off+10:], 4)
		copy(dst[off+12:], r.address[:])
		off += 16
	}
	return off
}

func templateReconstruct(dst, wire, patches []byte, q *dnswire.Message, elapsed uint32) int {
	copy(dst, wire)
	binary.BigEndian.PutUint16(dst, q.Question.Header.ID)
	flags := binary.BigEndian.Uint16(dst[2:])&^(dnswire.FlagAD|dnswire.FlagRD|dnswire.FlagCD) | q.Question.Header.Flags&(dnswire.FlagRD|dnswire.FlagCD)
	binary.BigEndian.PutUint16(dst[2:], flags)
	copy(dst[12:], q.Question.Name.Wire[:q.Question.Name.Length])
	for i := 0; i < len(patches); i += 8 {
		p := patches[i:]
		binary.BigEndian.PutUint32(dst[binary.BigEndian.Uint16(p):], binary.BigEndian.Uint32(p[4:])-elapsed)
	}
	return len(wire)
}

// Compare reconstruction only: both prototypes exclude lookup/key/expiry/locks,
// use prevalidated input and caller output, and perform the same personalization.
// Record counts distinguish small household replies from answer-heavy messages.
func BenchmarkRepresentation(b *testing.B) {
	for _, count := range []int{1, 6, 32} {
		b.Run(fmt.Sprintf("A%d", count), func(b *testing.B) {
			q, _ := fixture(b, "example.org.")
			m := new(dns.Msg)
			m.SetQuestion("example.org.", dns.TypeA)
			m.Response, m.Compress = true, true
			for i := 0; i < count; i++ {
				m.Answer = append(m.Answer, &dns.A{Hdr: dns.RR_Header{Name: "example.org.", Rrtype: 1, Class: 1, Ttl: uint32(120 + i)}, A: net.IPv4(192, 0, 2, byte(i+1))})
			}
			w, err := m.Pack()
			require.NoError(b, err)
			table := make([]byte, count*8)
			info, err := dnswire.PrepareTemplate(w, table, 300)
			require.NoError(b, err)
			require.Equal(b, count, info.Patches)
			compact := compactA{question: append([]byte(nil), w[12:q.Question.End]...), records: make([]compactRecord, count)}
			copy(compact.header[:], w[:12])
			for i := range compact.records {
				compact.records[i] = compactRecord{ttl: uint32(120 + i), address: [4]byte{192, 0, 2, byte(i + 1)}}
			}
			out := make([]byte, len(w))
			expected := make([]byte, len(w))
			n := compact.reconstruct(out, &q, 10)
			templateReconstruct(expected, w, table, &q, 10)
			require.Equal(b, expected, out[:n])
			var oracle dns.Msg
			require.NoError(b, oracle.Unpack(out[:n]))
			require.Len(b, oracle.Answer, count)
			b.Run("template", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					benchLength = templateReconstruct(out, w, table, &q, 10)
				}
				b.ReportMetric(float64(len(w)+len(table)), "payload-B")
			})
			b.Run("compact", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					benchLength = compact.reconstruct(out, &q, 10)
				}
				b.ReportMetric(float64(12+len(compact.question)+len(compact.records)*int(unsafe.Sizeof(compactRecord{}))), "payload-B")
			})
		})
	}
}
