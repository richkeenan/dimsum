package dnswire_test

import (
	"testing"

	"github.com/miekg/dns"
	"github.com/richkeenan/dimsum/internal/dnswire"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSyntheticContinuationRelocatesKnownNames(t *testing.T) {
	q := new(dns.Msg)
	q.SetQuestion("target.test.", 1)
	wire, e := q.Pack()
	require.NoError(t, e)
	var request dnswire.Message
	require.NoError(t, dnswire.ParseRequest(wire, &request))
	m := new(dns.Msg)
	m.SetReply(q)
	m.Compress = true
	for _, text := range []string{
		"target.test. 30 IN A 192.0.2.1", "target.test. 30 IN AAAA 2001:db8::1", "target.test. 30 IN CNAME next.test.",
		"test. 30 IN DNAME example.", "target.test. 30 IN PTR host.test.", "test. 30 IN NS ns.test.",
		"test. 30 IN MX 10 mail.test.", "_service._tcp.test. 30 IN SRV 1 2 443 server.test.",
		"target.test. 30 IN TXT \"opaque text\"", "target.test. 30 IN HTTPS 0 next.test.", "target.test. 30 IN SVCB 1 service.test. alpn=h2",
	} {
		rr, e := dns.NewRR(text)
		require.NoError(t, e)
		m.Answer = append(m.Answer, rr)
	}
	soa, e := dns.NewRR("test. 23 IN SOA ns.test. hostmaster.test. 1 3600 600 86400 23")
	require.NoError(t, e)
	m.Ns = []dns.RR{soa}
	wire, e = m.Pack()
	require.NoError(t, e)
	answers, authority, e := dnswire.SyntheticSections(wire)
	require.NoError(t, e)
	out := make([]byte, 65535)
	n, e := dnswire.BuildSynthetic(out, &request, 0, false, answers, authority)
	require.NoError(t, e)
	var got dns.Msg
	require.NoError(t, got.Unpack(out[:n]))
	require.Len(t, got.Answer, len(m.Answer))
	for i := range m.Answer {
		assert.Equal(t, m.Answer[i].String(), got.Answer[i].String())
	}
	require.Len(t, got.Ns, 1)
	assert.Equal(t, soa.String(), got.Ns[0].String())
	m.Answer = []dns.RR{&dns.RFC3597{Hdr: dns.RR_Header{Name: "target.test.", Rrtype: 65400, Class: 1}, Rdata: "c00c"}}
	wire, e = m.Pack()
	require.NoError(t, e)
	_, _, e = dnswire.SyntheticSections(wire)
	assert.ErrorIs(t, e, dnswire.ErrUnsupported)
	signature, e := dns.NewRR("target.test. 30 IN RRSIG A 8 2 30 20270101000000 20260101000000 1234 test. AQ==")
	require.NoError(t, e)
	m.Answer = []dns.RR{signature}
	wire, e = m.Pack()
	require.NoError(t, e)
	answers, _, e = dnswire.SyntheticSections(wire)
	require.NoError(t, e)
	assert.Empty(t, answers)
}
