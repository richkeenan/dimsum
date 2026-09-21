package dnscache

import (
	"bytes"
	"github.com/miekg/dns"
	"github.com/richkeenan/dimsum/internal/dnswire"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
	"time"
)

func FuzzReconstruction(f *testing.F) {
	_, w := fixture(f, "example.org.")
	f.Add(w, uint16(42), uint32(5))
	f.Fuzz(func(t *testing.T, wire []byte, id uint16, elapsed uint32) {
		if len(wire) > MaxMessage {
			return
		}
		var m dnswire.Message
		if dnswire.ScanMessage(wire, &m) != nil {
			return
		}
		// Build a matching independently parsed ordinary request from the
		// response's decoded question; admission still validates response names.
		query := make([]byte, 12+int(m.Question.Name.Length)+4)
		query[4], query[5] = 0, 1
		copy(query[12:], m.Question.Name.Wire[:m.Question.Name.Length])
		end := len(query)
		query[end-4], query[end-3] = byte(m.Question.Type>>8), byte(m.Question.Type)
		query[end-2], query[end-1] = byte(m.Question.Class>>8), byte(m.Question.Class)
		query[3] = byte(m.Question.Header.Flags & dnswire.FlagCD)
		var q dnswire.Message
		if dnswire.ParseRequest(query, &q) != nil {
			return
		}
		if m.EDNS.Present {
			return
		} // seeded full EDNS covered deterministically
		k, ok := NewKey(&q, 1, 1, 0)
		if !ok {
			return
		}
		c, err := New(Config{Bytes: 64 << 10, Shards: 1})
		require.NoError(t, err)
		now := time.Now()
		if !c.Put(k, wire, now) {
			return
		}
		var before dns.Msg
		oracleErr := before.Unpack(wire)
		q.Question.Header.ID = id
		for i, b := range q.Question.Name.Wire[:q.Question.Name.Length] {
			if b >= 'a' && b <= 'z' {
				q.Question.Name.Wire[i] = b - 32
			}
		}
		out := bytes.Repeat([]byte{0xcc}, MaxMessage)
		r := c.Get(k, &q, out, now.Add(time.Duration(elapsed)*time.Second))
		if !r.Hit {
			assert.Equal(t, bytes.Repeat([]byte{0xcc}, MaxMessage), out)
			return
		}
		var validated dnswire.Message
		require.NoError(t, dnswire.ScanMessage(out[:r.Length], &validated))
		// The oracle understands types that the production scanner intentionally
		// treats as opaque (e.g. NINFO). Its rejection of the original cannot be
		// used to judge reconstruction. Still verify unchanged record layout and
		// opaque/embedded RDATA before skipping the independent graph comparison.
		var bs, as dnswire.Scanner
		require.NoError(t, bs.Init(wire))
		require.NoError(t, as.Init(out[:r.Length]))
		var br, ar dnswire.Record
		for bs.Next(&br) {
			require.True(t, as.Next(&ar))
			assert.Equal(t, br.Start, ar.Start)
			assert.Equal(t, br.End, ar.End)
			assert.Equal(t, br.Name.Canonical, ar.Name.Canonical)
			assert.Equal(t, br.RData, ar.RData)
		}
		require.NoError(t, bs.Err())
		require.False(t, as.Next(&ar))
		require.NoError(t, as.Err())
		if oracleErr != nil {
			return
		}
		var after dns.Msg
		require.NoError(t, after.Unpack(out[:r.Length]))
		assert.Equal(t, id, after.Id)
		assert.False(t, after.AuthenticatedData)
		assert.Equal(t, len(before.Answer), len(after.Answer))
		assert.Equal(t, len(before.Ns), len(after.Ns))
		assert.Equal(t, len(before.Extra), len(after.Extra))
		// Normalise only the deliberately changed fields and compare oracle RR
		// graphs, including opaque RDATA. Names are compared case-insensitively.
		for section, rs := range [][]dns.RR{before.Answer, before.Ns, before.Extra} {
			got := [][]dns.RR{after.Answer, after.Ns, after.Extra}[section]
			for i, rr := range rs {
				assert.LessOrEqual(t, got[i].Header().Ttl, rr.Header().Ttl)
				if !r.Negative {
					assert.Equal(t, rr.Header().Ttl-elapsed, got[i].Header().Ttl)
				}
				rr.Header().Ttl = got[i].Header().Ttl
				assert.True(t, strings.EqualFold(rr.String(), got[i].String()), "%s != %s", rr.String(), got[i].String())
			}
		}
	})
}
