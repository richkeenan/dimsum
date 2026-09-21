package dnswire_test

import (
	"github.com/miekg/dns"
	"github.com/richkeenan/dimsum/internal/dnswire"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/dns/dnsmessage"
	"strings"
	"testing"
)

func buildQuery(t testing.TB, typ uint16) dnswire.Message {
	t.Helper()
	q := new(dns.Msg)
	q.SetQuestion("MiXeD.example.", typ)
	q.Id = 0x1234
	q.CheckingDisabled = true
	q.AuthenticatedData = true
	q.SetEdns0(4096, true)
	raw, err := q.Pack()
	require.NoError(t, err)
	var m dnswire.Message
	require.NoError(t, dnswire.ParseRequest(raw, &m))
	return m
}

func TestBuildReplies(t *testing.T) {
	for _, typ := range []uint16{1, 28, 65} {
		for _, rcode := range []uint16{0, 3, 5, 16} {
			m := buildQuery(t, typ)
			out := make([]byte, 1232)
			n, err := dnswire.BuildReply(out, &m, dnswire.Reply{RCode: rcode, Null: true, TTL: 2, RecursionAvailable: true}, 1232)
			require.NoError(t, err)
			var got dns.Msg
			require.NoError(t, got.Unpack(out[:n]))
			var second dnsmessage.Message
			require.NoError(t, second.Unpack(out[:n]))
			assert.Equal(t, 0x1234, int(got.Id))
			assert.Equal(t, "MiXeD.example.", got.Question[0].Name)
			assert.Equal(t, int(rcode), got.Rcode)
			assert.False(t, got.AuthenticatedData)
			assert.True(t, got.CheckingDisabled)
			assert.True(t, got.RecursionAvailable)
			require.NotNil(t, got.IsEdns0())
			assert.True(t, got.IsEdns0().Do())
			assert.Equal(t, uint16(1232), got.IsEdns0().UDPSize())
			want := 0
			if rcode == 0 && (typ == 1 || typ == 28) {
				want = 1
			}
			assert.Len(t, got.Answer, want)
			if want == 1 {
				assert.Equal(t, uint32(2), got.Answer[0].Header().Ttl)
			}
		}
	}
	m := buildQuery(t, 1)
	var out [512]byte
	n, err := dnswire.BuildReply(out[:], &m, dnswire.Reply{}, 1232)
	require.NoError(t, err)
	var got dns.Msg
	require.NoError(t, got.Unpack(out[:n]))
	assert.Empty(t, got.Answer)
	_, err = dnswire.BuildReply(out[:10], &m, dnswire.Reply{}, 1232)
	assert.Error(t, err)
}

func TestBuildBudgetsAndSafeTruncation(t *testing.T) {
	m := buildQuery(t, 1)
	for _, tc := range []struct {
		present bool
		size    uint16
		want    int
	}{{false, 4096, 512}, {true, 0, 512}, {true, 511, 512}, {true, 512, 512}, {true, 4096, 1232}} {
		m.EDNS.Present = tc.present
		m.EDNS.UDPSize = tc.size
		assert.Equal(t, tc.want, dnswire.UDPBudget(m.EDNS, 1232))
	}
	m = buildQuery(t, 1)
	q := new(dns.Msg)
	q.SetQuestion("MiXeD.example.", 1)
	q.Response = true
	q.Id = 0x1234
	q.Compress = true
	for i := 0; i < 50; i++ {
		q.Answer = append(q.Answer, &dns.CNAME{Hdr: dns.RR_Header{Name: "MiXeD.example.", Rrtype: 5, Class: 1}, Target: "alias.example."})
	}
	raw, err := q.Pack()
	require.NoError(t, err)
	require.Greater(t, len(raw), 512)
	out := make([]byte, 65535)
	n, err := dnswire.FitReply(out, raw, &m, 512, 1232)
	require.NoError(t, err)
	var got dns.Msg
	require.NoError(t, got.Unpack(out[:n]))
	assert.True(t, got.Truncated)
	assert.Empty(t, got.Answer)
	assert.Equal(t, "MiXeD.example.", got.Question[0].Name)
	var second dnsmessage.Message
	require.NoError(t, second.Unpack(out[:n]))
	n, err = dnswire.FitReply(out, raw, &m, 65535, 1232)
	require.NoError(t, err)
	assert.Equal(t, raw, out[:n])
}

func TestBuildOwnershipAndAllocations(t *testing.T) {
	m := buildQuery(t, 1)
	clear(m.Question.Original)
	clear(m.EDNS.Options)
	var out [1232]byte
	var n int
	var err error
	allocs := testing.AllocsPerRun(1000, func() { n, err = dnswire.BuildReply(out[:], &m, dnswire.Reply{Null: true, TTL: 2}, 1232) })
	require.NoError(t, err)
	assert.Zero(t, allocs)
	var got dns.Msg
	require.NoError(t, got.Unpack(out[:n]))
	assert.Equal(t, "MiXeD.example.", got.Question[0].Name)
}

func TestBuildCompressedQuestionAndOpaqueTruncation(t *testing.T) {
	// A compressed question pointing at a root byte in the original ID must
	// remain root even when the response header is rewritten.
	raw := []byte{0, 42, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0xc0, 0, 0, 1, 0, 1}
	var m dnswire.Message
	require.NoError(t, dnswire.ParseRequest(raw, &m))
	var out [65535]byte
	n, e := dnswire.BuildReply(out[:], &m, dnswire.Reply{Null: true}, 1232)
	require.NoError(t, e)
	var got dns.Msg
	require.NoError(t, got.Unpack(out[:n]))
	assert.Equal(t, ".", got.Question[0].Name)
	// Unknown RDATA is opaque; pointer-looking bytes must never be relocated.
	q := new(dns.Msg)
	q.SetQuestion("MiXeD.example.", 65000)
	q.Response = true
	q.Answer = []dns.RR{&dns.RFC3597{Hdr: dns.RR_Header{Name: "MiXeD.example.", Rrtype: 65000, Class: 1}, Rdata: strings.Repeat("c0ff", 400)}}
	raw, e = q.Pack()
	require.NoError(t, e)
	m = buildQuery(t, 65000)
	n, e = dnswire.FitReply(out[:], raw, &m, 512, 1232)
	require.NoError(t, e)
	require.NoError(t, got.Unpack(out[:n]))
	assert.True(t, got.Truncated)
	assert.Empty(t, got.Answer)
}

func BenchmarkBuildNull(b *testing.B) {
	m := buildQuery(b, 1)
	var out [1232]byte
	var err error
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, err = dnswire.BuildReply(out[:], &m, dnswire.Reply{Null: true, TTL: 2}, 1232)
	}
	b.StopTimer()
	require.NoError(b, err)
}
