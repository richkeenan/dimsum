package dnscache_test

import (
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/richkeenan/dimsum/internal/dnscache"
	"github.com/richkeenan/dimsum/internal/dnswire"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCNAMECrossZoneNegativeCacheExpiry(t *testing.T) {
	for _, tc := range []struct {
		name             string
		aliasTTL, soaTTL uint32
		minimum, cap     uint32
		lifetime         uint32
	}{
		{"alias-expires-first", 30, 200, 60, 300, 30},
		{"soa-minimum", 120, 200, 60, 300, 60},
		{"soa-ttl", 120, 20, 60, 300, 20},
		{"configured-cap", 120, 200, 60, 15, 15},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := new(dns.Msg)
			q.SetQuestion("alias.example.com.", dns.TypeHTTPS)
			q.SetEdns0(1232, false)
			request, err := q.Pack()
			require.NoError(t, err)
			var parsed dnswire.Message
			require.NoError(t, dnswire.ParseRequest(request, &parsed))
			key, ok := dnscache.NewKey(&parsed, 1, 1, 0)
			require.True(t, ok)
			m := new(dns.Msg)
			m.SetReply(q)
			m.Compress, m.RecursionAvailable = true, true
			m.Answer = []dns.RR{&dns.CNAME{Hdr: dns.RR_Header{Name: "alias.example.com.", Rrtype: dns.TypeCNAME, Class: dns.ClassINET, Ttl: tc.aliasTTL}, Target: "target.example.net."}}
			m.Ns = []dns.RR{&dns.SOA{Hdr: dns.RR_Header{Name: "example.net.", Rrtype: dns.TypeSOA, Class: dns.ClassINET, Ttl: tc.soaTTL}, Ns: "ns.example.net.", Mbox: "hostmaster.example.net.", Serial: 1, Minttl: tc.minimum}}
			m.SetEdns0(1232, false)
			wire, err := m.Pack()
			require.NoError(t, err)
			c, err := dnscache.New(dnscache.Config{Bytes: 128 << 10, Shards: 1, MaxNegativeTTL: tc.cap})
			require.NoError(t, err)
			now := time.Now()
			require.True(t, c.Put(key, wire, now))
			out := make([]byte, 4096)
			age := time.Duration(tc.lifetime-1) * time.Second
			hit := c.Get(key, &parsed, out, now.Add(age))
			require.True(t, hit.Hit)
			assert.True(t, hit.Negative)
			var got dns.Msg
			require.NoError(t, got.Unpack(out[:hit.Length]))
			require.Len(t, got.Answer, 1)
			require.Len(t, got.Ns, 1)
			assert.Equal(t, "target.example.net.", got.Answer[0].(*dns.CNAME).Target)
			assert.Equal(t, tc.aliasTTL-(tc.lifetime-1), got.Answer[0].Header().Ttl)
			assert.Equal(t, min(tc.soaTTL, tc.minimum, tc.cap)-(tc.lifetime-1), got.Ns[0].Header().Ttl)
			expired := now.Add(time.Duration(tc.lifetime) * time.Second)
			assert.False(t, c.Get(key, &parsed, out, expired).Hit)
			require.True(t, c.Put(key, wire, now), "restore the expired entry to exercise stale lookup independently")
			assert.False(t, c.Lookup(key, &parsed, out, expired, 3600, 30).Hit, "negative answers must not become stale positives")
		})
	}
}
