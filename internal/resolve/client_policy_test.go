package resolve

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/richkeenan/dimsum/internal/config"
	"github.com/richkeenan/dimsum/internal/dnswire"
	"github.com/richkeenan/dimsum/internal/lists"
	"github.com/richkeenan/dimsum/internal/localdns"
	"github.com/richkeenan/dimsum/internal/testutil"
	"github.com/richkeenan/dimsum/internal/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

func clientServer(t *testing.T, ip, alias string) *testutil.Upstream {
	return gatedClientServer(t, ip, alias, nil)
}

func gatedClientServer(t *testing.T, ip, alias string, gate <-chan struct{}) *testutil.Upstream {
	t.Helper()
	u, err := testutil.NewUpstream(testutil.NewClock(time.Now()), func(r testutil.Request) testutil.Response {
		if gate != nil {
			<-gate
		}
		var q dns.Msg
		if q.Unpack(r.Wire) != nil {
			return testutil.Response{Drop: true}
		}
		m := new(dns.Msg).SetReply(&q)
		name := q.Question[0].Name
		if alias != "" && name != alias {
			m.Answer = append(m.Answer, &dns.CNAME{Hdr: dns.RR_Header{Name: name, Rrtype: dns.TypeCNAME, Class: 1, Ttl: 60}, Target: alias})
			name = alias
		}
		m.Answer = append(m.Answer, &dns.A{Hdr: dns.RR_Header{Name: name, Rrtype: dns.TypeA, Class: 1, Ttl: 60}, A: net.ParseIP(ip)})
		wire, err := m.Pack()
		return testutil.Response{Wire: wire, Drop: err != nil}
	})
	require.NoError(t, err)
	t.Cleanup(func() { u.Close() })
	return u
}

func TestClientCoalescingAndCapturedGeneration(t *testing.T) {
	gate := make(chan struct{})
	a := gatedClientServer(t, "192.0.2.10", "blocked.example.", gate)
	b := gatedClientServer(t, "192.0.2.20", "blocked.example.", gate)
	p := clientPipeline(t, a.Address(), fmt.Sprintf(`clients:
  - id: alternate
    selectors: {addresses: [192.0.2.2]}
    overrides: {upstream: {upstreams: ['%s']}}
  - id: restricted
    selectors: {addresses: [192.0.2.3]}
    overrides:
      rules: [{id: deny, kind: exact, action: deny, pattern: blocked.example, enabled: true}]
`, b.Address()))
	// Release handlers even on a prerequisite failure, before pipeline cleanup.
	released := false
	defer func() {
		if !released {
			close(gate)
		}
	}()
	type result struct {
		request *transport.Request
		wire    []byte
		err     error
	}
	done := make(chan result, 3)
	for _, ip := range []string{"192.0.2.1", "192.0.2.2", "192.0.2.3"} {
		r := clientRequest(t, ip, "coalesced.example.", false)
		go func() {
			out := make([]byte, 65535)
			n, err := p.Resolve(t.Context(), r, out)
			done <- result{r, out[:n], err}
		}()
	}
	require.Eventually(t, func() bool {
		p.cache.flights.mu.Lock()
		defer p.cache.flights.mu.Unlock()
		waiters := 0
		for _, f := range p.cache.flights.active {
			waiters += f.waiters
		}
		return len(p.cache.flights.active) == 2 && waiters == 3
	}, time.Second, time.Millisecond)
	old := p.store.Snapshot()
	c := old.Config()
	c.Clients[1].Overrides.Rules = nil
	saveClientConfig(t, p, c)
	close(gate)
	released = true
	joined := 0
	for range 3 {
		var got result
		select {
		case got = <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("resolution did not complete")
		}
		require.NoError(t, got.err)
		assert.Equal(t, old.Generation(), got.request.Result.Generation)
		if got.request.Result.Coalesced {
			joined++
		}
		var m dns.Msg
		require.NoError(t, m.Unpack(got.wire))
		switch got.request.Peer.Addr().String() {
		case "192.0.2.3":
			assert.Equal(t, transport.PolicyBlock, got.request.Result.Outcome)
			assert.Equal(t, "restricted", got.request.Result.Rule.Scope.ID)
		case "192.0.2.2":
			assert.Equal(t, "192.0.2.20", m.Answer[1].(*dns.A).A.String())
		default:
			assert.Equal(t, "192.0.2.10", m.Answer[1].(*dns.A).A.String())
		}
	}
	assert.Equal(t, 1, joined, "only identical routes share work")
	r := clientRequest(t, "192.0.2.3", "coalesced.example.", false)
	clientAnswer(t, p, r)
	assert.NotEqual(t, transport.PolicyBlock, r.Result.Outcome, "new requests use updated policy")
}

func TestClientRoutePoolBoundedAcrossReloads(t *testing.T) {
	u := clientServer(t, "192.0.2.10", "")
	p := clientPipeline(t, u.Address(), "")
	var retired []*upstreamLease
	for round := range 3 {
		c := p.store.Snapshot().Config()
		c.Clients = nil
		for i := range 63 {
			c.Clients = append(c.Clients, config.ClientOverride{
				ID:        fmt.Sprintf("client-%d", i),
				Selectors: config.ClientSelectors{Addresses: []string{fmt.Sprintf("192.0.2.%d", i+2)}},
				Overrides: config.PolicyOverrides{Upstream: &config.UpstreamRoute{Upstreams: []string{u.Address()}, Fallback: []string{fmt.Sprintf("198.51.100.%d:%d", i+1, 10000+round)}}},
			})
		}
		saveClientConfig(t, p, c)
		for i := range 64 {
			clientAnswer(t, p, clientRequest(t, fmt.Sprintf("192.0.2.%d", i+1), "pool.example.", true))
			select {
			case <-u.Requests():
			case <-time.After(time.Second):
				t.Fatal("upstream request missing")
			}
		}
		p.upstreams.mu.Lock()
		count := len(p.upstreams.routes)
		var current []*upstreamLease
		for _, entry := range p.upstreams.routes {
			if entry != p.upstreams.current {
				current = append(current, entry)
			}
		}
		p.upstreams.mu.Unlock()
		assert.Equal(t, 64, count)
		for _, entry := range retired {
			_, err := entry.client.Exchange(t.Context(), nil, nil)
			assert.ErrorIs(t, err, net.ErrClosed)
		}
		retired = current
	}
	require.NoError(t, p.Close())
	for _, entry := range retired {
		_, err := entry.client.Exchange(t.Context(), nil, nil)
		assert.ErrorIs(t, err, net.ErrClosed)
	}
}

func saveClientConfig(t testing.TB, p *Pipeline, c config.Config) {
	t.Helper()
	b, err := yaml.Marshal(c)
	require.NoError(t, err)
	d, err := config.Parse(b)
	require.NoError(t, err)
	_, err = p.store.Save(t.Context(), p.store.Inspect().SavedRevision, d)
	require.NoError(t, err)
}

func TestClientProfileGenerationsBlockingAndPause(t *testing.T) {
	u := clientServer(t, "192.0.2.10", "blocked.example.")
	p := clientPipeline(t, u.Address(), `profiles:
  - id: restricted
    policy:
      rules: [{id: deny, kind: exact, action: deny, pattern: blocked.example, enabled: true}]
clients:
  - id: child
    profile: restricted
    selectors: {addresses: [192.0.2.1]}
  - id: exception
    profile: restricted
    selectors: {addresses: [192.0.2.2]}
    overrides: {blocking: false}
`)
	check := func(child, exception, network bool) {
		t.Helper()
		for i, ip := range []string{"192.0.2.1", "192.0.2.2", "192.0.2.3"} {
			for _, name := range []string{"blocked.example.", "alias.example."} {
				r := clientRequest(t, ip, name, false)
				clientAnswer(t, p, r)
				assert.Equal(t, []bool{child, exception, network}[i], r.Result.Outcome == transport.PolicyBlock, "%s %s", ip, name)
			}
		}
	}
	check(true, false, false)
	held := p.store.Snapshot()
	c := held.Config()
	c.Rules = c.Profiles[0].Policy.Rules
	c.Profiles[0].Policy.Rules = nil
	saveClientConfig(t, p, c)
	check(true, false, true)
	assert.NotEqual(t, held.Generation(), p.store.Snapshot().Generation())
	assert.Same(t, held.RouteContext(1), p.store.Snapshot().RouteContext(1))
	c.Blocking = new(bool)
	saveClientConfig(t, p, c)
	check(false, false, false)
	yes := true
	c.Clients[0].Overrides.Blocking = &yes
	saveClientConfig(t, p, c)
	check(true, false, false)
	until := time.Now().Add(time.Hour)
	c.Clients[0].PausedUntil = &until
	saveClientConfig(t, p, c)
	check(false, false, false)
	past := time.Now().Add(-time.Second)
	c.Clients[0].PausedUntil = &past
	saveClientConfig(t, p, c)
	check(true, false, false)
	c.Filtering.PauseUntil = until
	saveClientConfig(t, p, c)
	check(false, false, false)
}

func TestClientRouteRemovalCancelsWithoutNewQuery(t *testing.T) {
	a := clientServer(t, "192.0.2.10", "")
	b, err := testutil.NewUpstream(testutil.NewClock(time.Now()), func(testutil.Request) testutil.Response { return testutil.Response{Drop: true} })
	require.NoError(t, err)
	t.Cleanup(func() { b.Close() })
	p := clientPipeline(t, a.Address(), fmt.Sprintf(`clients:
  - id: alternate
    selectors: {addresses: [192.0.2.2]}
    overrides: {upstream: {upstreams: ['%s']}}
`, b.Address()))
	r := clientRequest(t, "192.0.2.2", "pending.example.", true)
	done := make(chan error, 1)
	go func() { _, err := p.Resolve(t.Context(), r, make([]byte, 65535)); done <- err }()
	select {
	case <-b.Requests():
	case <-time.After(time.Second):
		t.Fatal("alternate route not used")
	}
	old := p.store.Snapshot()
	effective := old.ClientPolicies().Select(r.Peer.Addr(), "")
	lifetime := old.RouteContext(effective.RouteKey())
	p.upstreams.mu.Lock()
	entry := p.upstreams.routes[lifetime]
	p.upstreams.mu.Unlock()
	require.NotNil(t, entry)
	c := old.Config()
	c.Clients[0].Overrides.Upstream = nil
	saveClientConfig(t, p, c)
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(time.Second):
		t.Fatal("removed route remains active")
	}
	require.Eventually(t, func() bool {
		p.upstreams.mu.Lock()
		defer p.upstreams.mu.Unlock()
		return len(p.upstreams.routes) == 0
	}, time.Second, time.Millisecond)
	_, err = entry.client.Exchange(t.Context(), nil, nil)
	assert.ErrorIs(t, err, net.ErrClosed)
	saveClientConfig(t, p, old.Config())
	assert.NotSame(t, lifetime, p.store.Snapshot().RouteContext(effective.RouteKey()))
	_, err = p.exchange(t.Context(), old, effective.RouteKey(), r.Wire, make([]byte, 65535))
	assert.ErrorIs(t, err, context.Canceled)
}

func TestClientRetainedOldRouteDoesNotReplaceNetworkHealth(t *testing.T) {
	a := clientServer(t, "192.0.2.10", "")
	b := clientServer(t, "192.0.2.20", "")
	p := clientPipeline(t, a.Address(), "")
	old := p.store.Snapshot()
	c := old.Config()
	c.Profiles = []config.Profile{{ID: "retained", Policy: config.PolicyOverrides{Upstream: &config.UpstreamRoute{Upstreams: []string{a.Address()}}}}}
	c.DNS.Upstreams = []string{b.Address()}
	saveClientConfig(t, p, c)
	r := clientRequest(t, "192.0.2.1", "health.example.", true)
	clientAnswer(t, p, r)
	current := p.upstreams.current
	_, err := p.exchange(t.Context(), old, old.ClientPolicies().Network().RouteKey(), r.Wire, make([]byte, 65535))
	require.NoError(t, err, "held queries may use a route still present in the new configuration")
	assert.Same(t, current, p.upstreams.current, "old captured route must not replace current network health")
}

func TestClientStaleRoutesAndResponsePolicy(t *testing.T) {
	for _, mode := range []string{"immediate", "failure-only"} {
		t.Run(mode, func(t *testing.T) {
			a := clientServer(t, "192.0.2.10", "blocked.example.")
			b := clientServer(t, "192.0.2.20", "blocked.example.")
			p := clientPipeline(t, a.Address(), fmt.Sprintf(`cache: {stale_mode: %s}
clients:
  - id: alternate
    selectors: {addresses: [192.0.2.2]}
    overrides: {upstream: {upstreams: ['%s']}}
  - id: restricted
    selectors: {addresses: [192.0.2.3]}
    overrides:
      rules: [{id: deny, kind: exact, action: deny, pattern: blocked.example, enabled: true}]
`, mode, b.Address()))
			// Seed both route namespaces from real authenticated replies, then age
			// the templates without wall-clock sleeps.
			for i, ip := range []string{"192.0.2.1", "192.0.2.2"} {
				r := clientRequest(t, ip, "stale-route.example.", false)
				m := clientAnswer(t, p, r)
				assert.Equal(t, []string{"192.0.2.10", "192.0.2.20"}[i], m.Answer[1].(*dns.A).A.String())
				wire, err := m.Pack()
				require.NoError(t, err)
				s := p.store.Snapshot()
				k, ok := cacheKeyRoute(r, s, s.ClientPolicies().Select(r.Peer.Addr(), "").RouteKey())
				require.True(t, ok)
				require.True(t, p.cache.cache.Put(k, wire, time.Now().Add(-61*time.Second)))
			}
			if mode == "failure-only" {
				a.Close()
				b.Close()
			}
			for i, ip := range []string{"192.0.2.1", "192.0.2.2", "192.0.2.3"} {
				r := clientRequest(t, ip, "stale-route.example.", false)
				m := clientAnswer(t, p, r)
				if i == 2 {
					assert.Equal(t, transport.PolicyBlock, r.Result.Outcome)
					assert.Equal(t, "restricted", r.Result.Rule.Scope.ID)
				} else {
					assert.Equal(t, transport.StaleAnswer, r.Result.Outcome)
					assert.Equal(t, []string{"192.0.2.10", "192.0.2.20"}[i], m.Answer[1].(*dns.A).A.String())
				}
			}
			if mode == "immediate" {
				require.Eventually(t, func() bool { return p.CacheStats().RefreshSuccess >= 2 }, time.Second, time.Millisecond)
				m := clientAnswer(t, p, clientRequest(t, "192.0.2.2", "stale-route.example.", false))
				assert.Equal(t, "192.0.2.20", m.Answer[1].(*dns.A).A.String())
			}
		})
	}
}

func TestClientAuthoritativeMACExpiryAndGeneration(t *testing.T) {
	u := clientServer(t, "192.0.2.10", "")
	p := clientPipeline(t, u.Address(), `clients:
  - id: mac-owner
    selectors: {macs: ['02:00:00:00:00:01']}
    overrides:
      rules: [{id: deny, kind: exact, action: deny, pattern: blocked.example, enabled: true}]
`)
	now := time.Now()
	for _, tc := range []struct {
		name       string
		generation uint64
		expiry     time.Time
		blocked    bool
	}{
		{"committed", p.store.Snapshot().Generation(), now.Add(time.Hour), true},
		{"expired", p.store.Snapshot().Generation(), now.Add(-time.Second), false},
		{"incompatible", p.store.Snapshot().Generation() + 1, now.Add(time.Hour), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := localdns.BuildLeases(tc.generation, "home.arpa", []localdns.Lease{{Address: netip.MustParseAddr("192.0.2.50"), MAC: "02:00:00:00:00:01", Hostname: "fixture", Expiry: tc.expiry}}, nil, nil)
			p.SetLeases(func(*config.Snapshot) *localdns.Leases { return v })
			r := clientRequest(t, "192.0.2.50", "blocked.example.", false)
			clientAnswer(t, p, r)
			assert.Equal(t, tc.blocked, r.Result.Outcome == transport.PolicyBlock)
		})
	}
}

func clientPipeline(t *testing.T, endpoint, extra string, options ...config.StoreOptions) *Pipeline {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	text := fmt.Sprintf("version: 1\ndns:\n  listen: ['127.0.0.1:0']\n  upstreams: ['%s']\nadmin: {listen: '127.0.0.1:0'}\npaths: {data_dir: data, secrets_dir: secrets}\n%s", endpoint, extra)
	require.NoError(t, os.WriteFile(path, []byte(text), 0600))
	option := config.StoreOptions{Offline: true}
	if len(options) > 0 {
		option = options[0]
	}
	s, err := config.OpenStore(t.Context(), path, filepath.Join(dir, "state"), option)
	require.NoError(t, err)
	p := NewWithStore(nil, s)
	t.Cleanup(func() { require.NoError(t, p.Close()) })
	return p
}

func TestClientListAssignmentDoesNotApplyToPeer(t *testing.T) {
	feed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("blocked.example\n")) }))
	t.Cleanup(feed.Close)
	u := clientServer(t, "192.0.2.10", "blocked.example.")
	p := clientPipeline(t, u.Address(), fmt.Sprintf(`lists:
  - {id: nsfw, url: '%s', dialect: domains, domain_kind: exact, enabled: true, default_apply: false}
clients:
  - id: phone
    name: Fixture phone
    selectors: {addresses: [192.0.2.1]}
    overrides: {lists: {nsfw: true}}
  - id: peer
    selectors: {addresses: [192.0.2.2]}
    overrides: {lists: {nsfw: false}}
`, feed.URL), config.StoreOptions{Fetcher: lists.NewFetcher(feed.Client())})
	check := func(network bool) {
		for i, ip := range []string{"192.0.2.1", "192.0.2.2", "192.0.2.3"} {
			for _, name := range []string{"blocked.example.", "alias.example."} {
				r := clientRequest(t, ip, name, false)
				clientAnswer(t, p, r)
				assert.Equal(t, []bool{true, false, network}[i], r.Result.Outcome == transport.PolicyBlock)
				if r.Result.Outcome == transport.PolicyBlock {
					assert.Equal(t, "nsfw", r.Result.Rule.SourceID)
				}
			}
		}
	}
	check(false)
	c := p.store.Snapshot().Config()
	yes := true
	c.Lists[0].DefaultApply = &yes
	saveClientConfig(t, p, c)
	check(true)
	assert.False(t, p.store.Snapshot().Config().Clients[1].Overrides.Lists["nsfw"], "explicit YAML exception survives a network-default edit")
}

func clientRequest(t testing.TB, ip, name string, bypass bool) *transport.Request {
	t.Helper()
	q := new(dns.Msg).SetQuestion(name, dns.TypeA)
	if bypass {
		q.SetEdns0(4096, false)
	}
	wire, err := q.Pack()
	require.NoError(t, err)
	r := &transport.Request{Wire: wire, Peer: netip.AddrPortFrom(netip.MustParseAddr(ip), 12345)}
	require.NoError(t, dnswire.ParseRequest(wire, &r.Message))
	return r
}

func clientAnswer(t *testing.T, p *Pipeline, r *transport.Request) dns.Msg {
	t.Helper()
	out := make([]byte, 65535)
	n, err := p.Resolve(context.Background(), r, out)
	require.NoError(t, err)
	var m dns.Msg
	require.NoError(t, m.Unpack(out[:n]))
	return m
}

func TestClientPolicyOriginalAndCachedAlias(t *testing.T) {
	u := clientServer(t, "192.0.2.10", "blocked.example.")
	p := clientPipeline(t, u.Address(), `clients:
  - id: restricted
    selectors: {addresses: [192.0.2.1]}
    overrides:
      rules: [{id: deny, kind: exact, action: deny, pattern: blocked.example, enabled: true}]
`)
	for _, name := range []string{"blocked.example.", "alias.example."} {
		for _, ip := range []string{"192.0.2.1", "192.0.2.2", "192.0.2.1"} {
			r := clientRequest(t, ip, name, false)
			m := clientAnswer(t, p, r)
			if ip == "192.0.2.1" {
				assert.Equal(t, transport.PolicyBlock, r.Result.Outcome)
				assert.Equal(t, "restricted", r.Result.Rule.Scope.ID)
				require.Len(t, m.Answer, 1)
				assert.Equal(t, "0.0.0.0", m.Answer[0].(*dns.A).A.String())
			} else {
				assert.NotEmpty(t, m.Answer)
			}
		}
	}
}

func TestClientRoutesFreshCachedBypassAndLocal(t *testing.T) {
	a := clientServer(t, "192.0.2.10", "")
	b := clientServer(t, "192.0.2.20", "")
	p := clientPipeline(t, a.Address(), fmt.Sprintf(`clients:
  - id: alternate
    selectors: {addresses: [192.0.2.2]}
    overrides:
      upstream: {upstreams: ['%s']}
records:
  - {name: local.home.arpa, type: CNAME, value: external.example, ttl: 30}
`, b.Address()))
	for _, name := range []string{"route.example.", "route.example.", "local.home.arpa."} {
		for _, bypass := range []bool{false, true} {
			for i, ip := range []string{"192.0.2.1", "192.0.2.2"} {
				r := clientRequest(t, ip, name, bypass)
				m := clientAnswer(t, p, r)
				if bypass || name == "local.home.arpa." {
					assert.EqualValues(t, 1+32*i, r.Result.UpstreamID, "generation-local endpoint IDs must distinguish routes")
				}
				require.NotEmpty(t, m.Answer)
				assert.Equal(t, []string{"192.0.2.10", "192.0.2.20"}[i], m.Answer[len(m.Answer)-1].(*dns.A).A.String())
			}
		}
	}
}
