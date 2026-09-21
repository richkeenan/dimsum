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
