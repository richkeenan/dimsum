package config

import (
	"fmt"
	"net/netip"
	"runtime"
	"testing"

	"github.com/richkeenan/dimsum/internal/lists"
	"github.com/richkeenan/dimsum/internal/policy"
	"github.com/stretchr/testify/require"
)

// One 100,000-rule subscription is compiled once. Common owners share a matcher;
// distinct owners each add one small custom rule, not a copy of the subscription.
func BenchmarkClientPolicyScaling(b *testing.B) {
	rules := make([]policy.Rule, 100_000)
	for i := range rules {
		rules[i] = policy.Rule{ID: fmt.Sprintf("feed:%d", i), SourceID: "feed", Kind: policy.Suffix, Class: policy.SubscriptionDeny, Pattern: fmt.Sprintf("ads%d.example", i)}
	}
	base, err := policy.CompileSnapshot(1, rules, policy.DefaultLimits())
	require.NoError(b, err)
	for _, count := range []int{1, 256, 4096} {
		for _, distinct := range []bool{false, true} {
			b.Run(fmt.Sprintf("clients=%d/distinct=%t", count, distinct), func(b *testing.B) {
				c := Default()
				c.Lists = []lists.Subscription{{ID: "feed", URL: "https://example.test/feed", Enabled: true, Dialect: "domains", DomainKind: "suffix"}}
				for i := range count {
					client := ClientOverride{ID: fmt.Sprintf("device-%d", i), Selectors: ClientSelectors{Addresses: []string{fmt.Sprintf("2001:db8::%x", i+1)}}}
					if distinct {
						client.Overrides.Rules = []CustomRule{{ID: "exception", Kind: policy.Exact, Action: "allow", Pattern: fmt.Sprintf("own%d.example", i), Enabled: true}}
					}
					c.Clients = append(c.Clients, client)
				}
				runtime.GC()
				var before, after runtime.MemStats
				runtime.ReadMemStats(&before)
				view, err := c.CompileClientPolicies(1, base)
				require.NoError(b, err)
				runtime.GC()
				runtime.ReadMemStats(&after)
				retained := int64(after.HeapAlloc) - int64(before.HeapAlloc)
				matchers := map[*policy.PolicySnapshot]bool{view.Network().Policy(): true}
				for _, client := range view.clients {
					matchers[client.Policy()] = true
				}
				want := 1
				if distinct {
					want += count
				}
				require.Len(b, matchers, want)
				addr := netip.MustParseAddr(fmt.Sprintf("2001:db8::%x", count))
				name, err := policy.NormalizeName("ads99999.example")
				require.NoError(b, err)
				require.Equal(b, policy.Block, view.Select(addr, "").Policy().Match(name).Result)
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					view.Select(addr, "").Policy().Match(name)
				}
				b.StopTimer()
				b.ReportMetric(float64(len(matchers)), "matchers")
				b.ReportMetric(float64(retained), "view-retained-B")
				runtime.KeepAlive(view)
				runtime.KeepAlive(c)
				runtime.KeepAlive(base)
			})
		}
	}
}
