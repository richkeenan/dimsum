package localdns_test

import (
	"testing"

	"github.com/miekg/dns"
	"github.com/richkeenan/dimsum/internal/dnswire"
	"github.com/richkeenan/dimsum/internal/localdns"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func localReply(t *testing.T, z *localdns.Zones, name string, typ uint16) *dns.Msg {
	t.Helper()
	q := new(dns.Msg)
	q.SetQuestion(name, typ)
	wire, err := q.Pack()
	require.NoError(t, err)
	var request dnswire.Message
	require.NoError(t, dnswire.ParseRequest(wire, &request))
	out := make([]byte, 65535)
	n, handled, err := z.Answer(out, &request)
	require.NoError(t, err)
	require.True(t, handled)
	got := new(dns.Msg)
	require.NoError(t, got.Unpack(out[:n]))
	return got
}

func TestLocalCNAMEToGeneratedApexSOA(t *testing.T) {
	z, err := localdns.Build([]localdns.Zone{{Name: "home.arpa", NegativeTTL: 42}}, []localdns.Record{
		{Name: "alias.home.arpa", Type: "CNAME", Value: "home.arpa", TTL: 12},
		{Name: "chain.home.arpa", Type: "CNAME", Value: "alias.home.arpa", TTL: 13},
	})
	require.NoError(t, err)
	for _, tc := range []struct {
		name  string
		count int
	}{{"home.arpa.", 1}, {"alias.home.arpa.", 2}, {"chain.home.arpa.", 3}} {
		t.Run(tc.name, func(t *testing.T) {
			got := localReply(t, z, tc.name, dns.TypeSOA)
			assert.Equal(t, dns.RcodeSuccess, got.Rcode)
			assert.Empty(t, got.Ns)
			require.Len(t, got.Answer, tc.count)
			for _, rr := range got.Answer[:tc.count-1] {
				assert.Equal(t, dns.TypeCNAME, rr.Header().Rrtype)
			}
			soa, ok := got.Answer[tc.count-1].(*dns.SOA)
			require.True(t, ok)
			assert.Equal(t, "home.arpa.", soa.Hdr.Name)
			assert.EqualValues(t, 42, soa.Hdr.Ttl)
			assert.EqualValues(t, 42, soa.Minttl)
		})
	}
}
