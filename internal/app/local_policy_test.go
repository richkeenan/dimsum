package app_test

import (
	"context"
	"fmt"
	"github.com/miekg/dns"
	"github.com/richkeenan/dimsum/internal/app"
	"github.com/richkeenan/dimsum/internal/config"
	"github.com/richkeenan/dimsum/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestManagedLocalAliasSpecialAndPause(t *testing.T) {
	workerErrors := make(chan error, 8)
	u, err := testutil.NewUpstream(testutil.NewClock(time.Now()), func(r testutil.Request) testutil.Response {
		var q dns.Msg
		if e := q.Unpack(r.Wire); e != nil {
			workerErrors <- e
			return testutil.Response{Drop: true}
		}
		m := new(dns.Msg)
		m.SetReply(&q)
		m.AuthenticatedData = true
		m.Answer = []dns.RR{&dns.CNAME{Hdr: dns.RR_Header{Name: q.Question[0].Name, Rrtype: 5, Class: 1, Ttl: 30}, Target: "ads.test."}}
		b, e := m.Pack()
		if e != nil {
			workerErrors <- e
		}
		return testutil.Response{Wire: b}
	})
	require.NoError(t, err)
	defer u.Close()
	for _, paused := range []bool{false, true} {
		t.Run(fmt.Sprint(paused), func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.yaml")
			pause := ""
			if paused {
				pause = "  pause_until: " + time.Now().Add(time.Hour).Format(time.RFC3339) + "\n"
			}
			text := fmt.Sprintf(`version: 1
dns:
  listen: [127.0.0.1:0]
  upstreams: [%s]
admin: {listen: '127.0.0.1:0'}
paths: {data_dir: './data', secrets_dir: './secrets'}
filtering:
  mozilla_canary: true
  private_relay: true
  designated_resolver: true
%szones:
  - {name: home.arpa, negative_ttl: 30}
records:
  - {name: ads.test, type: A, value: 192.168.1.9, ttl: 30}
rules:
  - {id: deny, action: deny, kind: exact, pattern: ads.test, enabled: true}
  - {id: allow, action: allow, kind: exact, pattern: allowed.test, enabled: true}
`, u.Address(), pause)
			require.NoError(t, os.WriteFile(path, []byte(text), 0600))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			store, e := config.OpenStore(ctx, path, filepath.Join(dir, "state"), config.StoreOptions{})
			require.NoError(t, e)
			service := new(app.Service)
			require.NoError(t, service.StartManaged(ctx, store))
			defer service.Close()
			for _, network := range []string{"udp", "tcp"} {
				for _, tc := range []struct {
					name  string
					typ   uint16
					rd    bool
					code  int
					first uint16
				}{
					{"ads.test.", 1, false, 0, 1}, {"missing.home.arpa.", 1, true, 3, 0}, {"2.1.168.192.in-addr.arpa.", 12, true, 3, 0},
					{"start.test.", 1, true, 0, 1}, {"allowed.test.", 1, true, 0, 5},
					{"use-application-dns.net.", 1, true, 3, 0}, {"mask.icloud.com.", 65, true, 3, 0}, {"_dns.resolver.arpa.", 64, true, 0, 0},
				} {
					q := new(dns.Msg)
					q.SetQuestion(tc.name, tc.typ)
					q.RecursionDesired = tc.rd
					q.AuthenticatedData = true
					client := dns.Client{Net: network, Timeout: time.Second}
					got, _, e := client.Exchange(q, service.Addresses().DNS[0])
					require.NoError(t, e)
					code, first := tc.code, tc.first
					if paused && (tc.name == "start.test." || tc.name == "use-application-dns.net." || tc.name == "mask.icloud.com." || tc.name == "_dns.resolver.arpa.") {
						code = 0
						first = 5
					}
					assert.Equal(t, code, got.Rcode, tc.name)
					assert.False(t, got.AuthenticatedData, tc.name)
					if first != 0 {
						require.NotEmpty(t, got.Answer, tc.name)
						assert.Equal(t, first, got.Answer[0].Header().Rrtype, tc.name)
					} else {
						assert.Empty(t, got.Answer, tc.name)
						require.Len(t, got.Ns, 1, tc.name)
					}
					if tc.name == "ads.test." {
						assert.Equal(t, "192.168.1.9", got.Answer[0].(*dns.A).A.String())
					}
				}
			}
		})
	}
	select {
	case e := <-workerErrors:
		assert.NoError(t, e)
	default:
	}
}
