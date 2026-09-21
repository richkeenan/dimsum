package localdns_test

import (
	"github.com/miekg/dns"
	"github.com/richkeenan/dimsum/internal/dnswire"
	"github.com/richkeenan/dimsum/internal/localdns"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"net/netip"
	"testing"
)

func TestZones(t *testing.T) {
	z, err := localdns.Build([]localdns.Zone{{Name: "home.arpa", NegativeTTL: 42}}, []localdns.Record{
		{Name: "host.home.arpa", Type: "A", Value: "192.168.1.2", TTL: 30},
		{Name: "host.home.arpa", Type: "AAAA", Value: "fd00::2", TTL: 30},
		{Name: "alias.home.arpa", Type: "CNAME", Value: "host.home.arpa", TTL: 30},
		{Name: "2.1.168.192.in-addr.arpa", Type: "PTR", Value: "host.home.arpa", TTL: 30},
		{Name: "leaf.branch.home.arpa", Type: "A", Value: "192.168.1.3"},
	})
	require.NoError(t, err)
	for _, tc := range []struct {
		name          string
		typ           uint16
		code, answers int
		handled       bool
	}{
		{"host.home.arpa.", 1, 0, 1, true}, {"host.home.arpa.", 28, 0, 1, true}, {"alias.home.arpa.", 1, 0, 2, true},
		{"2.1.168.192.in-addr.arpa.", 12, 0, 1, true}, {"missing.home.arpa.", 1, 3, 0, true},
		{"host.home.arpa.", 65, 0, 0, true}, {"branch.home.arpa.", 1, 0, 0, true}, {"home.arpa.", 1, 0, 0, true},
		{"outside.example.", 1, 0, 0, false},
		{"home.arpa.", 6, 0, 1, true},
	} {
		t.Run(tc.name+dns.TypeToString[tc.typ], func(t *testing.T) {
			q := new(dns.Msg)
			q.SetQuestion(tc.name, tc.typ)
			q.AuthenticatedData = true
			b, e := q.Pack()
			require.NoError(t, e)
			var m dnswire.Message
			require.NoError(t, dnswire.ParseRequest(b, &m))
			out := make([]byte, 65535)
			n, handled, e := z.Answer(out, &m)
			require.NoError(t, e)
			require.Equal(t, tc.handled, handled)
			if !handled {
				return
			}
			var got dns.Msg
			require.NoError(t, got.Unpack(out[:n]))
			assert.Equal(t, tc.code, got.Rcode)
			assert.Len(t, got.Answer, tc.answers)
			assert.False(t, got.AuthenticatedData)
			if tc.answers == 0 {
				require.Len(t, got.Ns, 1)
				soa := got.Ns[0].(*dns.SOA)
				assert.EqualValues(t, 42, soa.Minttl)
				assert.EqualValues(t, 42, soa.Hdr.Ttl)
			}
		})
	}
}
func TestRejectLocalConflictsAndLoops(t *testing.T) {
	for _, records := range [][]localdns.Record{
		{{Name: "a.home.arpa", Type: "CNAME", Value: "b.home.arpa"}, {Name: "b.home.arpa", Type: "CNAME", Value: "a.home.arpa"}},
		{{Name: "a.home.arpa", Type: "CNAME", Value: "b.home.arpa"}, {Name: "a.home.arpa", Type: "A", Value: "192.168.1.2"}},
	} {
		_, err := localdns.Build(nil, records)
		assert.Error(t, err)
	}
}

func TestExplicitSuffixRewriteAndAutoPTR(t *testing.T) {
	z, e := localdns.Build(nil, []localdns.Record{{Name: "lab.test", Type: "A", Value: "192.168.1.5", Match: "suffix", TTL: 12}, {Name: "exact.lab.test", Type: "AAAA", Value: "fd00::5", AutoPTR: true, TTL: 13}})
	require.NoError(t, e)
	for _, tc := range []struct {
		name    string
		typ     uint16
		answer  string
		handled bool
	}{
		{"a.b.lab.test.", 1, "192.168.1.5", true}, {"lab.test.", 1, "192.168.1.5", true}, {"a\\046b.lab.test.", 1, "192.168.1.5", true},
		{"notlab.test.", 1, "", false}, {"exact.lab.test.", 1, "", true},
		{localdns.Reverse(netip.MustParseAddr("fd00::5")) + ".", 12, "exact.lab.test.", true},
	} {
		q := new(dns.Msg)
		q.SetQuestion(tc.name, tc.typ)
		b, e := q.Pack()
		require.NoError(t, e)
		var request dnswire.Message
		require.NoError(t, dnswire.ParseRequest(b, &request))
		out := make([]byte, 65535)
		n, handled, e := z.Answer(out, &request)
		require.NoError(t, e)
		require.Equal(t, tc.handled, handled)
		if !handled {
			continue
		}
		var got dns.Msg
		require.NoError(t, got.Unpack(out[:n]))
		if tc.answer == "" {
			assert.Empty(t, got.Answer)
			assert.Len(t, got.Ns, 1)
		} else {
			require.Len(t, got.Answer, 1)
			assert.Contains(t, got.Answer[0].String(), tc.answer)
			assert.Equal(t, got.Question[0].Name, got.Answer[0].Header().Name)
		}
	}
}
