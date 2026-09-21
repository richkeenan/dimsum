package resolve_test

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/richkeenan/dimsum/internal/config"
	"github.com/richkeenan/dimsum/internal/dnswire"
	"github.com/richkeenan/dimsum/internal/resolve"
	"github.com/richkeenan/dimsum/internal/testutil"
	"github.com/richkeenan/dimsum/internal/transport"
	"github.com/richkeenan/dimsum/internal/upstream"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Each upstream answer carries its call number so bypasses and generation
// changes can be distinguished from a personalized copy of an earlier answer.
func cacheIntegrationUpstream(t *testing.T, cname bool) (*testutil.Upstream, *atomic.Int32) {
	t.Helper()
	calls := new(atomic.Int32)
	workerErrors := make(chan error, 32)
	u, err := testutil.NewUpstream(testutil.NewClock(time.Now()), func(r testutil.Request) testutil.Response {
		call := calls.Add(1)
		var q dns.Msg
		if err := q.Unpack(r.Wire); err != nil {
			workerErrors <- err
			return testutil.Response{Drop: true}
		}
		reply := new(dns.Msg)
		reply.SetReply(&q)
		reply.RecursionAvailable = true
		reply.AuthenticatedData = true
		name := q.Question[0].Name
		if cname {
			reply.Answer = append(reply.Answer, &dns.CNAME{Hdr: dns.RR_Header{Name: name, Rrtype: dns.TypeCNAME, Class: dns.ClassINET, Ttl: 60}, Target: "blocked.example."})
			name = "blocked.example."
		}
		reply.Answer = append(reply.Answer, &dns.A{Hdr: dns.RR_Header{Name: name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60}, A: net.IPv4(192, 0, 2, byte(call))})
		if opt := q.IsEdns0(); opt != nil {
			reply.SetEdns0(1232, opt.Do())
		}
		wire, err := reply.Pack()
		if err != nil {
			workerErrors <- err
		}
		return testutil.Response{Wire: wire}
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, u.Close())
		close(workerErrors)
		for err := range workerErrors {
			assert.NoError(t, err)
		}
	})
	return u, calls
}

func cacheIntegrationStore(t *testing.T, u *testutil.Upstream, extra string) (*config.Store, string, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	text := fmt.Sprintf("version: 1\ndns:\n  listen: [127.0.0.1:0]\n  upstreams: [%s]\nadmin: {listen: '127.0.0.1:0'}\npaths: {data_dir: data, secrets_dir: secrets}\n", u.Address())
	require.NoError(t, os.WriteFile(path, []byte(text+extra), 0600))
	store, err := config.OpenStore(context.Background(), path, filepath.Join(dir, "state"), config.StoreOptions{Offline: true})
	require.NoError(t, err)
	return store, path, text
}

func cacheIntegrationResolve(t *testing.T, p *resolve.Pipeline, q *dns.Msg) *dns.Msg {
	t.Helper()
	wire, err := q.Pack()
	require.NoError(t, err)
	original := append([]byte(nil), wire...)
	r := transport.Request{Wire: wire}
	require.NoError(t, dnswire.ParseRequest(wire, &r.Message))
	out := make([]byte, 65535)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	n, err := p.Resolve(ctx, &r, out)
	require.NoError(t, err)
	got := new(dns.Msg)
	require.NoError(t, got.Unpack(out[:n]))
	assert.Equal(t, original, wire, "borrowed request must remain unchanged")
	assert.Equal(t, q.Id, got.Id)
	assert.Equal(t, q.Question, got.Question)
	assert.Equal(t, q.RecursionDesired, got.RecursionDesired)
	assert.False(t, got.AuthenticatedData)
	return got
}

func cacheIntegrationAddress(t *testing.T, got *dns.Msg, want string) {
	t.Helper()
	assert.Equal(t, dns.RcodeSuccess, got.Rcode)
	require.Len(t, got.Answer, 1)
	a, ok := got.Answer[0].(*dns.A)
	require.True(t, ok, "expected A answer, got %T", got.Answer[0])
	assert.Equal(t, want, a.A.String())
}

func TestCacheIntegrationFreshPersonalization(t *testing.T) {
	for _, managed := range []bool{false, true} {
		t.Run(fmt.Sprintf("managed=%t", managed), func(t *testing.T) {
			u, calls := cacheIntegrationUpstream(t, false)
			var p *resolve.Pipeline
			if managed {
				store, _, _ := cacheIntegrationStore(t, u, "")
				p = resolve.NewWithStore(nil, store)
			} else {
				c, err := upstream.New(upstream.Options{Endpoints: []netip.AddrPort{netip.MustParseAddrPort(u.Address())}})
				require.NoError(t, err)
				p = resolve.New(c)
			}
			t.Cleanup(func() { assert.NoError(t, p.Close()) })
			q := new(dns.Msg)
			q.SetQuestion("MiXeD.example.", dns.TypeA)
			q.Id = 101
			cacheIntegrationAddress(t, cacheIntegrationResolve(t, p, q), "192.0.2.1")
			q.Question[0].Name = "mIxEd.EXAMPLE."
			q.Id = 202
			cacheIntegrationAddress(t, cacheIntegrationResolve(t, p, q), "192.0.2.1")
			q.Question[0].Name = "MIXED.example."
			q.Id = 303
			q.RecursionDesired = false
			cacheIntegrationAddress(t, cacheIntegrationResolve(t, p, q), "192.0.2.1")
			assert.EqualValues(t, 1, calls.Load(), "fresh answers, including RD=0, must reuse one upstream exchange")
		})
	}
}

func TestCacheIntegrationReloadIsolatesGeneration(t *testing.T) {
	u, calls := cacheIntegrationUpstream(t, false)
	store, path, text := cacheIntegrationStore(t, u, "")
	p := resolve.NewWithStore(nil, store)
	t.Cleanup(func() { assert.NoError(t, p.Close()) })
	q := new(dns.Msg)
	q.SetQuestion("cached.example.", dns.TypeA)
	cacheIntegrationAddress(t, cacheIntegrationResolve(t, p, q), "192.0.2.1")
	cacheIntegrationAddress(t, cacheIntegrationResolve(t, p, q), "192.0.2.1")
	assert.EqualValues(t, 1, calls.Load())
	old := store.Snapshot().Generation()
	// An unrelated policy change still advances the cache's generation boundary.
	require.NoError(t, os.WriteFile(path, []byte(text+"rules:\n  - {id: deny, kind: exact, action: deny, pattern: unrelated.example, enabled: true}\n"), 0600))
	_, err := store.Reload(context.Background())
	require.NoError(t, err)
	require.Greater(t, store.Snapshot().Generation(), old)
	cacheIntegrationAddress(t, cacheIntegrationResolve(t, p, q), "192.0.2.2")
	cacheIntegrationAddress(t, cacheIntegrationResolve(t, p, q), "192.0.2.2")
	assert.EqualValues(t, 2, calls.Load(), "new generation must miss once, then populate its own cache")
}

func TestCacheIntegrationHitRechecksCNAMEAfterPause(t *testing.T) {
	u, calls := cacheIntegrationUpstream(t, true)
	until := time.Now().Add(1500 * time.Millisecond)
	extra := fmt.Sprintf("filtering:\n  mode: nxdomain\n  pause_until: %s\nrules:\n  - {id: deny, kind: exact, action: deny, pattern: blocked.example, enabled: true}\n", until.Format(time.RFC3339Nano))
	store, _, _ := cacheIntegrationStore(t, u, extra)
	p := resolve.NewWithStore(nil, store)
	t.Cleanup(func() { assert.NoError(t, p.Close()) })
	generation := store.Snapshot().Generation()
	q := new(dns.Msg)
	q.SetQuestion("alias.example.", dns.TypeA)
	got := cacheIntegrationResolve(t, p, q)
	require.Equal(t, dns.RcodeSuccess, got.Rcode)
	require.Len(t, got.Answer, 2)
	got = cacheIntegrationResolve(t, p, q)
	assert.Equal(t, dns.RcodeSuccess, got.Rcode)
	assert.Len(t, got.Answer, 2)
	assert.EqualValues(t, 1, calls.Load(), "paused answer should be cached")
	time.Sleep(time.Until(until) + 20*time.Millisecond)
	got = cacheIntegrationResolve(t, p, q)
	assert.Equal(t, dns.RcodeNameError, got.Rcode, "cached CNAME target must be inspected using current pause time")
	assert.Empty(t, got.Answer)
	assert.Equal(t, generation, store.Snapshot().Generation(), "pause expiry must not rely on a reload")
	assert.EqualValues(t, 1, calls.Load(), "policy should block the cache hit without another exchange")
}

func TestCacheIntegrationConservativeEDNSBypass(t *testing.T) {
	for _, variant := range []string{"cookie", "unknown-option", "udp512", "udp4096"} {
		t.Run(variant, func(t *testing.T) {
			u, calls := cacheIntegrationUpstream(t, false)
			c, err := upstream.New(upstream.Options{Endpoints: []netip.AddrPort{netip.MustParseAddrPort(u.Address())}})
			require.NoError(t, err)
			p := resolve.New(c)
			t.Cleanup(func() { assert.NoError(t, p.Close()) })
			q := new(dns.Msg)
			q.SetQuestion("edns.example.", dns.TypeA)
			q.SetEdns0(1232, false)
			cacheIntegrationAddress(t, cacheIntegrationResolve(t, p, q), "192.0.2.1")
			unusual := q.Copy()
			switch variant {
			case "cookie":
				unusual.IsEdns0().Option = []dns.EDNS0{&dns.EDNS0_COOKIE{Code: dns.EDNS0COOKIE, Cookie: "0011223344556677"}}
			case "unknown-option":
				unusual.IsEdns0().Option = []dns.EDNS0{&dns.EDNS0_LOCAL{Code: 65001, Data: []byte{1, 2, 3}}}
			case "udp512":
				unusual.IsEdns0().SetUDPSize(512)
			case "udp4096":
				unusual.IsEdns0().SetUDPSize(4096)
			}
			cacheIntegrationAddress(t, cacheIntegrationResolve(t, p, unusual), "192.0.2.2")
			cacheIntegrationAddress(t, cacheIntegrationResolve(t, p, unusual), "192.0.2.3")
			cacheIntegrationAddress(t, cacheIntegrationResolve(t, p, q), "192.0.2.1")
			assert.EqualValues(t, 3, calls.Load(), "normalized variants must neither read nor overwrite the canonical cache entry")
		})
	}
}
