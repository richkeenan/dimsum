package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/richkeenan/dimsum/internal/admin"
	"github.com/richkeenan/dimsum/internal/control"
	"github.com/richkeenan/dimsum/internal/stats"
	"github.com/richkeenan/dimsum/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPerformanceHTTPReportsTimingsAndEmptyBuckets(t *testing.T) {
	h, _ := historyFixture(t)
	e := historyEvent(1, stats.FreshCache)
	e.Duration = 0
	require.NoError(t, h.db.WriteBatch(t.Context(), "b", []stats.QueryEvent{e}, storage.BatchOptions{Snapshot: historyCoverage(1)}))
	handler := admin.New(control.New(control.Options{Provider: h}), admin.Options{}).LocalHandler()
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/performance", nil))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var r historyPerformance
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &r))
	assert.True(t, r.Complete)
	assert.Equal(t, "1", r.Summary.Count)
	require.NotNil(t, r.Summary.AverageUS)
	assert.Equal(t, "0", *r.Summary.AverageUS)
	assert.Equal(t, "0", *r.Summary.P99US)
	assert.True(t, r.Summary.PercentilesAvailable)
	require.Len(t, r.Points, 60)
	assert.Nil(t, r.Points[1].P95US)
	assert.False(t, r.Points[1].Gap)
	require.Len(t, r.Outcomes, 6)
	assert.Equal(t, "cache", r.Outcomes[2].Outcome)
	assert.Equal(t, "1", r.Distribution[0].Count)
	assert.Nil(t, r.Distribution[7].UpperUS)
}

func TestPerformancePresentationDoesNotClaimLegacyPercentiles(t *testing.T) {
	l := storage.Latency{Count: 2, Duration: 1000}
	l.Fine[stats.LatencyIndex(500)] = 1
	r := presentLatency(l)
	assert.Equal(t, "500", *r.AverageUS)
	assert.False(t, r.PercentilesAvailable)
	assert.Nil(t, r.P50US)
	assert.Nil(t, r.P95US)
	assert.Nil(t, r.P99US)
}

func TestPerformanceValidatesRangesAndDependencies(t *testing.T) {
	h, _ := historyFixture(t)
	for _, q := range []url.Values{
		{"resolution_seconds": {"30"}}, {"resolution_seconds": {""}},
		{"outcome": {"cache"}}, {"from": {"bad"}},
		{"from": {historyEnd.Add(-48 * time.Hour).Format(time.RFC3339)}, "resolution_seconds": {"60"}},
		{"resolution_seconds": {"60", "3600"}},
	} {
		_, err := h.Performance(t.Context(), q)
		assert.ErrorIs(t, err, control.BadRequest)
	}
	v, err := h.Performance(t.Context(), url.Values{"from": {historyEnd.Add(-90 * 24 * time.Hour).Format(time.RFC3339)}})
	require.NoError(t, err)
	r := v.(historyPerformance)
	assert.Equal(t, int64(86400), r.ResolutionSeconds)
	assert.Nil(t, r.Summary.AverageUS)
	assert.True(t, r.Points[0].Gap)
	_, err = (&historyProvider{}).Performance(t.Context(), nil)
	assert.ErrorIs(t, err, control.Unavailable)
}
