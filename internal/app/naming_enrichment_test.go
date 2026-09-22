package app

import (
	"encoding/json"
	"github.com/richkeenan/dimsum/internal/clients"
	"github.com/richkeenan/dimsum/internal/stats"
	"github.com/richkeenan/dimsum/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"net/netip"
	"net/url"
	"testing"
	"time"
)

func TestEnrichmentPreservesOwnerNameAcrossHistoryAPIs(t *testing.T) {
	h, _ := historyFixture(t)
	h.name = func(a netip.Addr) clients.Name {
		return clients.Name{Address: a, Name: "Owner label", Source: "override", Fresh: true, Device: &clients.Enrichment{Category: "tv", Reason: "Advertised television", Fresh: true, Evidence: []clients.Evidence{}}}
	}
	require.NoError(t, h.db.WriteBatch(t.Context(), "test", []stats.QueryEvent{historyEvent(1, stats.PolicyBlock)}, storage.BatchOptions{Snapshot: historyCoverage(1)}))
	for _, resource := range []string{"queries", "rankings", "clients"} {
		t.Run(resource, func(t *testing.T) {
			var v any
			var err error
			switch resource {
			case "queries":
				v, err = h.Queries(t.Context(), url.Values{})
			case "rankings":
				v, err = h.Rankings(t.Context(), url.Values{})
			case "clients":
				v, err = h.Clients(t.Context(), url.Values{})
			}
			require.NoError(t, err)
			b, err := json.Marshal(v)
			require.NoError(t, err)
			assert.Contains(t, string(b), `"category":"tv"`)
			assert.Contains(t, string(b), `Owner label`)
		})
	}
}

func TestDHCPHistoryExposesExpiryWithoutMergingIPIdentities(t *testing.T) {
	h, _ := historyFixture(t)
	expires := historyEnd.Add(time.Hour)
	h.name = func(a netip.Addr) clients.Name {
		return clients.Name{Address: a, Name: "shared.home.arpa", Source: "dhcp", Fresh: true, Expires: expires}
	}
	one, two := historyEvent(1, stats.FreshCache), historyEvent(2, stats.FreshCache)
	two.Client = netip.MustParseAddr("192.0.2.11").As16()
	require.NoError(t, h.db.WriteBatch(t.Context(), "test", []stats.QueryEvent{one, two}, storage.BatchOptions{Snapshot: historyCoverage(2)}))
	v, err := h.Clients(t.Context(), url.Values{})
	require.NoError(t, err)
	page := v.(historyClients)
	require.Len(t, page.Items, 2)
	addresses := []string{page.Items[0].Address, page.Items[1].Address}
	assert.ElementsMatch(t, []string{"192.0.2.10", "192.0.2.11"}, addresses)
	for _, item := range page.Items {
		assert.Equal(t, "1", item.Count)
		assert.Equal(t, "dhcp", item.NameSource)
		require.NotNil(t, item.NameExpires)
		assert.Equal(t, expires, *item.NameExpires)
	}
	v, err = h.Queries(t.Context(), url.Values{})
	require.NoError(t, err)
	queries := v.(historyQueries)
	require.Len(t, queries.Items, 2)
	for _, item := range queries.Items {
		require.NotNil(t, item.ClientNameExpires)
		assert.Equal(t, expires, *item.ClientNameExpires)
	}
}
