package app

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/richkeenan/dimsum/internal/config"
	"github.com/richkeenan/dimsum/internal/stats"
	"github.com/richkeenan/dimsum/internal/storage"
	"github.com/richkeenan/dimsum/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHistoryRecordsActualForwardedCachedAndLocalReplies(t *testing.T) {
	errors := make(chan error, 8)
	u, err := testutil.NewUpstream(testutil.NewClock(time.Now()), func(r testutil.Request) testutil.Response {
		var q dns.Msg
		if e := q.Unpack(r.Wire); e != nil {
			errors <- e
			return testutil.Response{Drop: true}
		}
		m := new(dns.Msg)
		m.SetReply(&q)
		m.RecursionAvailable = true
		if opt := q.IsEdns0(); opt != nil {
			m.SetEdns0(1232, opt.Do())
		}
		m.Answer = []dns.RR{&dns.A{Hdr: dns.RR_Header{Name: q.Question[0].Name, Rrtype: 1, Class: 1, Ttl: 45}, A: net.ParseIP("192.0.2.42")}}
		wire, e := m.Pack()
		if e != nil {
			errors <- e
		}
		return testutil.Response{Wire: wire}
	})
	require.NoError(t, err)
	defer u.Close()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	configText := fmt.Sprintf(`version: 1
dns: {listen: ['127.0.0.1:0'], upstreams: ['%s']}
admin: {listen: '127.0.0.1:0'}
paths: {data_dir: './data', secrets_dir: './secrets'}
zones: [{name: home.arpa, negative_ttl: 30}]
records: [{name: host.home.arpa, type: A, value: 192.0.2.43, ttl: 90}]
rules: [{id: block, action: deny, kind: exact, pattern: blocked.test, enabled: true}]
`, u.Address())
	require.NoError(t, os.WriteFile(path, []byte(configText), 0600))
	store, err := config.OpenStore(t.Context(), path, filepath.Join(dir, "state"), config.StoreOptions{Offline: true})
	require.NoError(t, err)
	s := new(Service)
	require.NoError(t, s.StartManaged(context.Background(), store))
	defer s.Close()
	client := dns.Client{Timeout: time.Second}
	var replies []*dns.Msg
	for _, name := range []string{"external.test.", "external.test.", "host.home.arpa.", "blocked.test."} {
		q := new(dns.Msg)
		q.SetQuestion(name, dns.TypeA)
		q.SetEdns0(1232, false)
		reply, _, e := client.Exchange(q, s.Addresses().DNS[0])
		require.NoError(t, e)
		replies = append(replies, reply)
	}
	var page storage.Page
	require.Eventually(t, func() bool {
		page, err = s.observability.db.Query(t.Context(), storage.QueryOptions{Start: time.Now().Add(-time.Minute), End: time.Now()})
		return err == nil && len(page.Rows) == 4
	}, 3*time.Second, 10*time.Millisecond)
	outcomes := []stats.Outcome{stats.ForwardedAnswer, stats.FreshCache, stats.LocalAnswer, stats.PolicyBlock}
	for i, reply := range replies {
		row := page.Rows[3-i]
		assert.Equal(t, outcomes[i], row.Event.Outcome)
		assert.EqualValues(t, reply.Rcode, row.Event.RCode)
		require.NotNil(t, row.Response)
		assert.False(t, row.Response.Truncated)
		var answers int
		for _, record := range row.Response.Records {
			if record.Section != "answer" {
				continue
			}
			require.Less(t, answers, len(reply.Answer))
			assert.Equal(t, reply.Answer[answers].Header().Ttl, record.TTL)
			if a, ok := reply.Answer[answers].(*dns.A); ok {
				assert.Equal(t, a.A.String(), record.Value)
			}
			answers++
		}
		assert.Equal(t, len(reply.Answer), answers)
	}
	select {
	case err := <-errors:
		assert.NoError(t, err)
	default:
	}
}
