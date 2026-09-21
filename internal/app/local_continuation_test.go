package app_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/richkeenan/dimsum/internal/app"
	"github.com/richkeenan/dimsum/internal/config"
	"github.com/richkeenan/dimsum/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocalCNAMECompletesExternalTarget(t *testing.T) {
	errors := make(chan error, 8)
	u, e := testutil.NewUpstream(testutil.NewClock(time.Now()), func(r testutil.Request) testutil.Response {
		var q dns.Msg
		if e := q.Unpack(r.Wire); e != nil {
			errors <- e
			return testutil.Response{Drop: true}
		}
		m := new(dns.Msg)
		m.SetReply(&q)
		m.AuthenticatedData = true
		m.Compress = true
		switch q.Question[0].Name {
		case "target.test.":
			m.Answer = []dns.RR{&dns.A{Hdr: dns.RR_Header{Name: "target.test.", Rrtype: 1, Class: 1, Ttl: 45}, A: net.ParseIP("192.0.2.42")}}
		case "loop.test.":
			m.Answer = []dns.RR{&dns.CNAME{Hdr: dns.RR_Header{Name: "loop.test.", Rrtype: 5, Class: 1, Ttl: 45}, Target: "loop.home.arpa."}}
		case "negative.test.":
			m.Rcode = 3
			m.Ns = []dns.RR{&dns.SOA{Hdr: dns.RR_Header{Name: "test.", Rrtype: 6, Class: 1, Ttl: 23}, Ns: "ns.test.", Mbox: "hostmaster.test.", Serial: 1, Refresh: 3600, Retry: 60, Expire: 86400, Minttl: 23}}
		}
		b, e := m.Pack()
		if e != nil {
			errors <- e
		}
		return testutil.Response{Wire: b}
	})
	require.NoError(t, e)
	defer u.Close()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	text := fmt.Sprintf(`version: 1
dns: {listen: ['127.0.0.1:0'], upstreams: ['%s']}
admin: {listen: '127.0.0.1:0'}
paths: {data_dir: './data', secrets_dir: './secrets'}
zones: [{name: home.arpa, negative_ttl: 30}]
records:
  - {name: alias.home.arpa, type: CNAME, value: target.test, ttl: 10}
  - {name: loop.home.arpa, type: CNAME, value: loop.test, ttl: 10}
  - {name: negative.home.arpa, type: CNAME, value: negative.test, ttl: 10}
  - {name: private.home.arpa, type: CNAME, value: 2.1.168.192.in-addr.arpa, ttl: 10}
rules:
  - {id: shadowed, action: deny, kind: exact, pattern: target.test, enabled: true}
`, u.Address())
	require.NoError(t, os.WriteFile(path, []byte(text), 0600))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store, e := config.OpenStore(ctx, path, filepath.Join(dir, "state"), config.StoreOptions{})
	require.NoError(t, e)
	s := new(app.Service)
	require.NoError(t, s.StartManaged(ctx, store))
	defer s.Close()
	for _, tc := range []struct {
		name          string
		rd            bool
		code, answers int
	}{{"alias.home.arpa.", true, 0, 2}, {"loop.home.arpa.", true, 2, 0}, {"negative.home.arpa.", true, 3, 1}, {"alias.home.arpa.", false, 0, 1}, {"private.home.arpa.", true, 3, 1}} {
		q := new(dns.Msg)
		q.SetQuestion(tc.name, 1)
		q.RecursionDesired = tc.rd
		q.SetEdns0(1232, true)
		c := dns.Client{Timeout: time.Second}
		got, _, e := c.Exchange(q, s.Addresses().DNS[0])
		require.NoError(t, e)
		assert.Equal(t, tc.code, got.Rcode, tc.name)
		require.Len(t, got.Answer, tc.answers, tc.name)
		assert.False(t, got.AuthenticatedData)
		if tc.answers == 2 {
			assert.Equal(t, "192.0.2.42", got.Answer[1].(*dns.A).A.String())
		}
		if tc.code == 3 {
			require.Len(t, got.Ns, 1)
			if tc.name == "negative.home.arpa." {
				assert.EqualValues(t, 23, got.Ns[0].(*dns.SOA).Minttl)
			}
		}
	}
	select {
	case e := <-errors:
		assert.NoError(t, e)
	default:
	}
}
