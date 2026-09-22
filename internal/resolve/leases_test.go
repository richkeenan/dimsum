package resolve

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/richkeenan/dimsum/internal/config"
	"github.com/richkeenan/dimsum/internal/dnswire"
	"github.com/richkeenan/dimsum/internal/localdns"
	"github.com/richkeenan/dimsum/internal/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPipelineLeaseCaptureDoesNotCacheOrMixGenerations(t *testing.T) {
	p, _, out := warmPipeline(t, true)
	g := p.store.Snapshot().Generation()
	build := func(gen uint64, expiry time.Time) *localdns.Leases {
		return localdns.BuildLeases(gen, "home.arpa", []localdns.Lease{{Address: netip.MustParseAddr("192.0.2.100"), Hostname: "desk", Expiry: expiry}}, nil, nil)
	}
	view := build(g, time.Now().Add(time.Minute))
	p.SetLeases(func(*config.Snapshot) *localdns.Leases { return view })
	q := new(dns.Msg)
	q.SetQuestion("desk.home.arpa.", 1)
	q.RecursionDesired = false
	b, e := q.Pack()
	require.NoError(t, e)
	r := transport.Request{Wire: b}
	require.NoError(t, dnswire.ParseRequest(b, &r.Message))
	for _, tc := range []struct {
		v             *localdns.Leases
		code, answers int
	}{
		{view, 0, 1}, {nil, 5, 0}, {view, 0, 1}, {build(g+1, time.Now().Add(time.Minute)), 5, 0}, {build(g, time.Now().Add(-time.Second)), 3, 0},
	} {
		view = tc.v
		n, e := p.Resolve(context.Background(), &r, out)
		require.NoError(t, e)
		var got dns.Msg
		require.NoError(t, got.Unpack(out[:n]))
		assert.Equal(t, tc.code, got.Rcode)
		assert.Len(t, got.Answer, tc.answers)
	}
}

func BenchmarkWarmManagedPipelineDHCP(b *testing.B) {
	p, r, out := warmPipeline(b, true)
	v := localdns.BuildLeases(p.store.Snapshot().Generation(), "home.arpa", []localdns.Lease{{Address: netip.MustParseAddr("192.0.2.100"), Hostname: "desk", Expiry: time.Now().Add(time.Hour)}}, nil, nil)
	p.SetLeases(func(*config.Snapshot) *localdns.Leases { return v })
	var n int
	var err error
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		n, err = p.Resolve(context.Background(), r, out)
		if err != nil {
			break
		}
	}
	b.StopTimer()
	require.NoError(b, err)
	require.Greater(b, n, 12)
	require.Equal(b, transport.FreshAnswer, r.Result.Outcome)
}

func TestDHCPPipelineCommonPathAllocations(t *testing.T) {
	for _, tc := range []struct {
		name    string
		typ     uint16
		outcome transport.Outcome
	}{
		{"www.example.test.", 1, transport.FreshAnswer},
		{"unrelated.test.", 1, transport.PolicyBlock},
		{"desk.home.arpa.", 1, transport.LocalAnswer},
		{"100.2.0.192.in-addr.arpa.", 12, transport.LocalAnswer},
		{"desk.home.arpa.", 28, transport.LocalAnswer},
		{"missing.home.arpa.", 1, transport.LocalAnswer},
	} {
		t.Run(tc.name+dns.TypeToString[tc.typ], func(t *testing.T) {
			p, _, out := warmPipeline(t, true)
			v := localdns.BuildLeases(p.store.Snapshot().Generation(), "home.arpa", []localdns.Lease{{Address: netip.MustParseAddr("192.0.2.100"), Hostname: "desk", Expiry: time.Now().Add(time.Minute)}}, nil, nil)
			q := new(dns.Msg)
			q.SetQuestion(tc.name, tc.typ)
			b, e := q.Pack()
			require.NoError(t, e)
			r := transport.Request{Wire: b}
			require.NoError(t, dnswire.ParseRequest(b, &r.Message))
			var n int
			budget := float64(1)
			if tc.outcome != transport.LocalAnswer {
				budget = testing.AllocsPerRun(100, func() { n, e = p.Resolve(context.Background(), &r, out) })
				require.NoError(t, e)
				require.Equal(t, tc.outcome, r.Result.Outcome)
			}
			p.SetLeases(func(*config.Snapshot) *localdns.Leases { return v })
			allocs := testing.AllocsPerRun(100, func() { n, e = p.Resolve(context.Background(), &r, out) })
			require.NoError(t, e)
			require.Greater(t, n, 12)
			assert.Equal(t, tc.outcome, r.Result.Outcome)
			assert.LessOrEqual(t, allocs, budget, "lease integration must not add allocations to existing common paths")
		})
	}
}

func TestExpiredLeasePTRReturnsToPrivateReversePolicy(t *testing.T) {
	p, _, out := warmPipeline(t, true)
	// A synthetic RFC1918 address is necessary to exercise private reverse policy.
	expires := time.Now().Add(time.Minute)
	view := localdns.BuildLeases(p.store.Snapshot().Generation(), "home.arpa", []localdns.Lease{{Address: netip.MustParseAddr("10.23.0.100"), Hostname: "desk", Expiry: expires}}, nil, nil)
	p.SetLeases(func(*config.Snapshot) *localdns.Leases { return view })
	q := new(dns.Msg)
	q.SetQuestion("100.0.23.10.in-addr.arpa.", 12)
	b, e := q.Pack()
	require.NoError(t, e)
	r := transport.Request{Wire: b}
	require.NoError(t, dnswire.ParseRequest(b, &r.Message))
	n, e := p.Resolve(context.Background(), &r, out)
	require.NoError(t, e)
	var got dns.Msg
	require.NoError(t, got.Unpack(out[:n]))
	require.Len(t, got.Answer, 1)
	assert.Equal(t, "desk.home.arpa.", got.Answer[0].(*dns.PTR).Ptr)
	view = localdns.BuildLeases(p.store.Snapshot().Generation(), "home.arpa", []localdns.Lease{{Address: netip.MustParseAddr("10.23.0.100"), Hostname: "desk", Expiry: time.Now().Add(-time.Second)}}, nil, nil)
	n, e = p.Resolve(context.Background(), &r, out)
	require.NoError(t, e)
	require.NoError(t, got.Unpack(out[:n]))
	assert.Equal(t, 3, got.Rcode)
	assert.Empty(t, got.Answer)
	assert.Equal(t, transport.PolicyBlock, r.Result.Outcome)
}
