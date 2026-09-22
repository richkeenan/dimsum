package queryresult

import (
	"net"
	"strings"
	"testing"

	"github.com/miekg/dns"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func packet(t *testing.T, records ...string) []byte {
	t.Helper()
	m := new(dns.Msg)
	m.SetQuestion("example.test.", dns.TypeA)
	m.Response = true
	m.Compress = true
	for _, text := range records {
		r, err := dns.NewRR(text)
		require.NoError(t, err)
		m.Answer = append(m.Answer, r)
	}
	wire, err := m.Pack()
	require.NoError(t, err)
	return wire
}

func TestSummaryPreservesAnswerChainAndIndividualTTLs(t *testing.T) {
	s := Summarize(packet(t,
		"example.test. 300 IN CNAME edge.example.test.",
		"edge.example.test. 42 IN A 192.0.2.9",
		"edge.example.test. 0 IN AAAA 2001:db8::9",
	), false)
	require.NotNil(t, s)
	assert.False(t, s.Truncated)
	assert.Equal(t, []Record{
		{Name: "example.test", Type: "CNAME", Value: "edge.example.test", TTL: 300, Section: "answer"},
		{Name: "edge.example.test", Type: "A", Value: "192.0.2.9", TTL: 42, Section: "answer"},
		{Name: "edge.example.test", Type: "AAAA", Value: "2001:db8::9", TTL: 0, Section: "answer"},
	}, s.Records)
}

func TestSummaryOtherRecordsAndNegativeAuthority(t *testing.T) {
	s := Summarize(packet(t,
		"example.test. 60 IN MX 10 mail.example.test.",
		"example.test. 61 IN TXT \"hello\" \"world\"",
		"example.test. 62 IN HTTPS 1 . alpn=\"h2\" port=443 ipv4hint=192.0.2.10",
	), false)
	require.NotNil(t, s)
	require.Len(t, s.Records, 3)
	assert.Equal(t, "10 mail.example.test", s.Records[0].Value)
	assert.Equal(t, `"hello" "world"`, s.Records[1].Value)
	assert.Equal(t, "1 . alpn=h2 port=443 ipv4hint=192.0.2.10", s.Records[2].Value)
	m := new(dns.Msg)
	m.SetQuestion("missing.test.", dns.TypeA)
	m.Response, m.Rcode = true, dns.RcodeNameError
	r, err := dns.NewRR("test. 120 IN SOA ns.test. hostmaster.test. 1 3600 600 86400 60")
	require.NoError(t, err)
	m.Ns = []dns.RR{r}
	wire, err := m.Pack()
	require.NoError(t, err)
	s = Summarize(wire, false)
	require.NotNil(t, s)
	require.Len(t, s.Records, 1)
	assert.Equal(t, "authority", s.Records[0].Section)
	assert.Equal(t, uint32(120), s.Records[0].TTL)
	assert.Equal(t, "ns.test hostmaster.test 1 3600 600 86400 60", s.Records[0].Value)
}

func TestSummaryBoundsAndUnavailableVersusEmpty(t *testing.T) {
	assert.Nil(t, Summarize([]byte{1, 2}, false))
	empty := Summarize(packet(t), false)
	require.NotNil(t, empty)
	assert.Empty(t, empty.Records)
	assert.False(t, empty.Truncated)
	m := new(dns.Msg)
	m.SetQuestion("example.test.", dns.TypeA)
	m.Response = true
	for i := 0; i < 100; i++ {
		m.Answer = append(m.Answer, &dns.A{Hdr: dns.RR_Header{Name: "example.test.", Rrtype: dns.TypeA, Class: 1, Ttl: 60}, A: net.IPv4(192, 0, 2, byte(i))})
	}
	wire, err := m.Pack()
	require.NoError(t, err)
	s := Summarize(wire, false)
	require.NotNil(t, s)
	assert.Len(t, s.Records, 16)
	assert.True(t, s.Truncated)
	short := packet(t, "example.test. 60 IN A 192.0.2.1", "example.test. 60 IN A 192.0.2.2")
	assert.Nil(t, Summarize(short[:len(short)-1], false), "malformed full captures must not look valid")
	s = Summarize(short[:len(short)-1], true)
	require.NotNil(t, s)
	require.Len(t, s.Records, 1)
	assert.True(t, s.Truncated)
	assert.Equal(t, "192.0.2.1", s.Records[0].Value)
	large := packet(t, "example.test. 60 IN TXT "+strings.Repeat(`"`+strings.Repeat("x", 250)+`" `, 8))
	s = Summarize(large, false)
	require.NotNil(t, s)
	require.Len(t, s.Records, 1)
	assert.LessOrEqual(t, len(s.Records[0].Value), 1024)
	assert.True(t, s.Truncated)
}
