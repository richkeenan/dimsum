package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/richkeenan/dimsum/internal/admin"
	"github.com/richkeenan/dimsum/internal/clients"
	"github.com/richkeenan/dimsum/internal/config"
	"github.com/richkeenan/dimsum/internal/control"
	"github.com/richkeenan/dimsum/internal/stats"
	"github.com/richkeenan/dimsum/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var historyEnd = time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
var historyStart = historyEnd.Add(-time.Hour)

func historyFixture(t *testing.T) (*historyProvider, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "history.sqlite")
	db, e := storage.Open(path)
	require.NoError(t, e)
	t.Cleanup(func() { assert.NoError(t, db.Close()) })
	h := NewHistoryProvider(db, func(address netip.Addr) clients.Name {
		return clients.Name{Address: address, Name: "Office", Source: "override", Fresh: true}
	}).(*historyProvider)
	h.now = func() time.Time { return historyEnd.Add(34 * time.Second) }
	return h, path
}
func historyEvent(seq uint64, outcome stats.Outcome) stats.QueryEvent {
	e := stats.QueryEvent{Sequence: seq, Timestamp: historyStart.Add(10 * time.Second).UnixMicro(), Duration: 123, Generation: 7, RuleID: 4, UpstreamID: 9, QType: 1, QClass: 1, Outcome: outcome, Client: netip.MustParseAddr("192.0.2.10").As16()}
	wire := []byte{3, 'a', 'd', 's', 7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 0}
	copy(e.QName[:], wire)
	e.QNameLength = uint8(len(wire))
	return e
}
func historyCoverage(n uint64) *stats.Snapshot {
	return &stats.Snapshot{ObservedStart: historyStart.UnixMicro(), ObservedEnd: historyEnd.UnixMicro(), Version: n, Sequence: n, Admitted: n}
}

func TestHistoryProviderRealSQLitePresentationAndConsistentDefaults(t *testing.T) {
	h, _ := historyFixture(t)
	events := []stats.QueryEvent{historyEvent(1, stats.PolicyBlock), historyEvent(2, stats.FreshCache), historyEvent(3, stats.StaleCache), historyEvent(4, stats.ResolutionError)}
	require.NoError(t, h.db.WriteBatch(t.Context(), "boot-a", events, storage.BatchOptions{Snapshot: historyCoverage(4), Rules: []storage.RuleVersion{{Generation: 7, RuleID: 4, Description: "exact deny ads.example"}}}))
	v, e := h.Summary(t.Context(), url.Values{})
	require.NoError(t, e)
	summary := v.(historySummary)
	assert.Equal(t, "4", summary.Queries)
	assert.Equal(t, "1", summary.Blocked)
	assert.Equal(t, "1", summary.Fresh)
	assert.Equal(t, "1", summary.Stale)
	assert.Equal(t, "492", summary.DurationUS)
	assert.True(t, summary.Complete)
	v, e = h.Queries(t.Context(), url.Values{})
	require.NoError(t, e)
	page := v.(historyQueries)
	require.Len(t, page.Items, 4)
	assert.Equal(t, summary.Range, page.Range)
	item := page.Items[0]
	assert.Equal(t, "ads.example", item.Name)
	assert.Equal(t, "192.0.2.10", item.Client)
	assert.Equal(t, "Office", item.ClientName)
	assert.Equal(t, "override", item.ClientNameSource)
	assert.Equal(t, "error", item.Outcome)
	assert.Equal(t, "123", item.DurationUS)
	assert.Equal(t, "A", item.QType)
	assert.Equal(t, "7", item.Generation)
	v, e = h.Rankings(t.Context(), url.Values{})
	require.NoError(t, e)
	rankings := v.(historyRankings)
	assert.Equal(t, summary.Range, rankings.Range)
	require.Len(t, rankings.Clients, 1)
	assert.Equal(t, rankedClient{"192.0.2.10", "Office", "4"}, rankings.Clients[0])
	assert.Equal(t, []rankedDomain{{"ads.example", "1"}}, rankings.Domains)
	v, e = h.Timeseries(t.Context(), url.Values{})
	require.NoError(t, e)
	series := v.(historySeries)
	assert.Equal(t, summary.Range, series.Range)
	require.Len(t, series.Points, 60)
	assert.Equal(t, "1", series.Points[0].Outcomes["blocked"])
	assert.Equal(t, "1", series.Points[0].Outcomes["error"])
	assert.Equal(t, "0", series.Points[1].Outcomes["blocked"])
	assert.False(t, series.Points[1].Gap)
	v, e = h.Clients(t.Context(), url.Values{})
	require.NoError(t, e)
	observed := v.(historyClients)
	assert.Equal(t, summary.Range, observed.Range)
	require.Len(t, observed.Items, 1)
	assert.Equal(t, "192.0.2.10", observed.Items[0].Address)
	assert.Equal(t, "Office", observed.Items[0].Name)
	assert.Equal(t, "4", observed.Items[0].Count)
	assert.Equal(t, "1", observed.Items[0].Blocked)
}

func TestHistoryCursorPinsDefaultWindowAndNormalizedFilters(t *testing.T) {
	h, _ := historyFixture(t)
	var events []stats.QueryEvent
	for i := uint64(1); i <= 5; i++ {
		events = append(events, historyEvent(i, stats.PolicyBlock))
	}
	require.NoError(t, h.db.WriteBatch(t.Context(), "boot", events))
	q := url.Values{"limit": {"2"}, "name": {"ADS.EXAMPLE."}, "client": {"::ffff:192.0.2.10"}, "qtype": {"A"}, "outcome": {"blocked"}}
	v, e := h.Queries(t.Context(), q)
	require.NoError(t, e)
	first := v.(historyQueries)
	require.Len(t, first.Items, 2)
	require.NotEmpty(t, first.NextCursor)
	assert.Equal(t, "5", first.Items[0].Sequence)
	require.NoError(t, h.db.WriteBatch(t.Context(), "boot", []stats.QueryEvent{historyEvent(6, stats.PolicyBlock)}))
	h.now = func() time.Time { return historyEnd.Add(20 * time.Minute) }
	q.Set("cursor", first.NextCursor)
	q.Set("client", "192.0.2.10")
	q.Set("qtype", "1")
	q.Set("name", "ads.example")
	v, e = h.Queries(t.Context(), q)
	require.NoError(t, e)
	second := v.(historyQueries)
	assert.Equal(t, first.Range, second.Range)
	require.Len(t, second.Items, 2)
	assert.Equal(t, "3", second.Items[0].Sequence)
	assert.Equal(t, "2", second.Items[1].Sequence)
	q.Set("cursor", second.NextCursor)
	v, e = h.Queries(t.Context(), q)
	require.NoError(t, e)
	last := v.(historyQueries)
	require.Len(t, last.Items, 1)
	assert.Equal(t, "1", last.Items[0].Sequence)
	assert.Empty(t, last.NextCursor)
	q.Set("cursor", first.NextCursor)
	q.Set("outcome", "cache")
	_, e = h.Queries(t.Context(), q)
	assert.ErrorIs(t, e, control.BadRequest)
	q.Set("outcome", "blocked")
	q.Set("from", historyStart.Add(time.Minute).Format(time.RFC3339))
	_, e = h.Queries(t.Context(), q)
	assert.ErrorIs(t, e, control.BadRequest)
	q.Del("from")
	q.Set("cursor", "invalid!")
	_, e = h.Queries(t.Context(), q)
	assert.ErrorIs(t, e, control.BadRequest)
}

func TestHistoryDetailBootScopedMetadataBinaryNamesAndRetention(t *testing.T) {
	h, _ := historyFixture(t)
	a := historyEvent(1, stats.PolicyBlock)
	a.QNameLength = 4
	copy(a.QName[:], []byte{2, '.', 255, 0})
	require.NoError(t, h.db.WriteBatch(t.Context(), "boot-a", []stats.QueryEvent{a}, storage.BatchOptions{Rules: []storage.RuleVersion{{Generation: 7, RuleID: 4, Description: "first boot rule"}}, Aliases: map[uint64][]byte{1: {1, 'x', 0}}}))
	require.NoError(t, h.db.WriteBatch(t.Context(), "boot-b", []stats.QueryEvent{historyEvent(1, stats.PolicyBlock)}, storage.BatchOptions{Rules: []storage.RuleVersion{{Generation: 7, RuleID: 4, Description: "second boot rule"}}}))
	v, e := h.Queries(t.Context(), url.Values{})
	require.NoError(t, e)
	page := v.(historyQueries)
	require.Len(t, page.Items, 2)
	v, e = h.Query(t.Context(), url.Values{"id": {page.Items[1].ID}})
	require.NoError(t, e)
	detail := v.(historyDetail)
	assert.Equal(t, `\046\255`, detail.Name)
	assert.Equal(t, "first boot rule", detail.RuleDescription)
	assert.True(t, detail.RuleDescriptionAvailable)
	assert.True(t, detail.AliasAvailable)
	require.NotNil(t, detail.Alias)
	assert.Equal(t, "x", *detail.Alias)
	assert.False(t, detail.AliasChainAvailable)
	assert.False(t, detail.UpstreamAttemptsAvailable)
	assert.False(t, detail.CacheAgeAvailable)
	assert.False(t, detail.ADTrustAvailable)
	v, e = h.Query(t.Context(), url.Values{"id": {page.Items[0].ID}})
	require.NoError(t, e)
	detail = v.(historyDetail)
	assert.Equal(t, "second boot rule", detail.RuleDescription)
	assert.False(t, detail.AliasAvailable)
	v, e = h.Queries(t.Context(), url.Values{"name": {`\046\255`}})
	require.NoError(t, e)
	require.Len(t, v.(historyQueries).Items, 1)
	_, e = h.Query(t.Context(), url.Values{"id": {"99999"}})
	assert.ErrorIs(t, e, control.NotFound)
	require.NoError(t, h.db.Retain(t.Context(), historyEnd.Add(8*24*time.Hour), storage.DefaultRetention()))
	_, e = h.Query(t.Context(), url.Values{"id": {page.Items[0].ID}})
	assert.ErrorIs(t, e, control.NotFound)
}

func TestHistoryLargeNumbersRemainStrings(t *testing.T) {
	h, path := historyFixture(t)
	require.NoError(t, h.db.WriteBatch(t.Context(), "boot", []stats.QueryEvent{historyEvent(1, stats.PolicyBlock)}))
	connection, e := sql.Open("sqlite", path)
	require.NoError(t, e)
	defer connection.Close()
	const large = int64(9007199254740993)
	_, e = connection.Exec("UPDATE sqlite_sequence SET seq=? WHERE name='query_events'", large-1)
	require.NoError(t, e)
	event := historyEvent(uint64(large), stats.PolicyBlock)
	require.NoError(t, h.db.WriteBatch(t.Context(), "boot", []stats.QueryEvent{event}))
	v, e := h.Query(t.Context(), url.Values{"id": {strconv.FormatInt(large, 10)}})
	require.NoError(t, e)
	b, e := json.Marshal(v)
	require.NoError(t, e)
	assert.Contains(t, string(b), `"id":"9007199254740993"`)
	assert.Contains(t, string(b), `"sequence":"9007199254740993"`)
	_, e = connection.Exec("UPDATE rollups SET count=?,duration=? WHERE resolution=3600", large, large)
	require.NoError(t, e)
	v, e = h.Summary(t.Context(), url.Values{})
	require.NoError(t, e)
	summary := v.(historySummary)
	assert.Equal(t, "9007199254740993", summary.Queries)
	assert.Equal(t, "9007199254740993", summary.DurationUS)
	v, e = h.Timeseries(t.Context(), url.Values{"resolution_seconds": {"3600"}})
	require.NoError(t, e)
	assert.Equal(t, "9007199254740993", v.(historySeries).Points[0].Outcomes["blocked"])
}

func TestHistoryValidationMissingDataAndCancellation(t *testing.T) {
	h, _ := historyFixture(t)
	for _, q := range []url.Values{{"from": {"bad"}}, {"to": {"2026-09-21T13:00:00+01:00"}}, {"from": {historyEnd.Format(time.RFC3339)}}, {"from": {historyEnd.Add(-367 * 24 * time.Hour).Format(time.RFC3339)}}, {"limit": {"0"}}, {"limit": {"201"}}, {"client": {"no-ip"}}, {"qtype": {"NOTATYPE"}}, {"outcome": {"zero"}}, {"name": {"bad..name"}}, {"unknown": {"x"}}, {"limit": {"1", "2"}}} {
		_, e := h.Queries(t.Context(), q)
		assert.ErrorIs(t, e, control.BadRequest, q)
	}
	_, e := h.Timeseries(t.Context(), url.Values{"resolution_seconds": {"30"}})
	assert.ErrorIs(t, e, control.BadRequest)
	_, e = h.Timeseries(t.Context(), url.Values{"from": {historyEnd.Add(-7 * 24 * time.Hour).Format(time.RFC3339)}, "resolution_seconds": {"60"}})
	assert.ErrorIs(t, e, control.BadRequest)
	_, e = h.Clients(t.Context(), url.Values{"limit": {"201"}})
	assert.ErrorIs(t, e, control.BadRequest)
	v, e := h.Timeseries(t.Context(), url.Values{})
	require.NoError(t, e)
	series := v.(historySeries)
	assert.False(t, series.Complete)
	require.Len(t, series.Points, 60)
	assert.True(t, series.Points[0].Gap)
	assert.Nil(t, series.Points[0].Outcomes)
	assert.Nil(t, series.Points[0].DurationUS)
	assert.Nil(t, series.Points[0].Histogram)
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	_, e = h.Summary(canceled, url.Values{})
	assert.ErrorIs(t, e, context.Canceled)
	assert.ErrorIs(t, e, control.Unavailable)
	missing := NewHistoryProvider(nil, nil)
	for _, method := range []func(context.Context, url.Values) (any, error){missing.Summary, missing.Queries, missing.Query, missing.Rankings, missing.Timeseries, missing.(control.ClientProvider).Clients} {
		_, e := method(t.Context(), url.Values{})
		assert.ErrorIs(t, e, control.Unavailable)
	}
	_, e = h.Query(t.Context(), url.Values{"id": {"01"}})
	assert.ErrorIs(t, e, control.BadRequest)
	_, e = h.Queries(t.Context(), url.Values{"cursor": {strings.Repeat("a", 2049)}})
	assert.ErrorIs(t, e, control.BadRequest)
}

func TestHistoryHalfOpenWindowAndObservedClientBound(t *testing.T) {
	h, _ := historyFixture(t)
	var events []stats.QueryEvent
	for i := uint64(1); i <= 202; i++ {
		event := historyEvent(i, stats.ForwardedAnswer)
		event.Client = netip.AddrFrom4([4]byte{192, 0, 2, byte(i)}).As16()
		events = append(events, event)
	}
	events[0].Timestamp = historyEnd.UnixMicro()
	require.NoError(t, h.db.WriteBatch(t.Context(), "boot", events))
	v, e := h.Summary(t.Context(), url.Values{})
	require.NoError(t, e)
	assert.Equal(t, "201", v.(historySummary).Queries)
	v, e = h.Clients(t.Context(), url.Values{"limit": {"200"}})
	require.NoError(t, e)
	page := v.(historyClients)
	assert.Len(t, page.Items, 200)
	assert.True(t, page.Truncated)
}

func TestHistoryTimeseriesExactPartialEdges(t *testing.T) {
	h, _ := historyFixture(t)
	events := []stats.QueryEvent{historyEvent(1, stats.PolicyBlock), historyEvent(2, stats.PolicyBlock), historyEvent(3, stats.PolicyBlock), historyEvent(4, stats.FreshCache)}
	events[0].Timestamp = historyStart.Add(4 * time.Second).UnixMicro()
	events[1].Timestamp = historyStart.Add(5 * time.Second).UnixMicro()
	events[2].Timestamp = historyStart.Add(15 * time.Second).UnixMicro()
	events[3].Timestamp = historyStart.Add(time.Minute + 10*time.Second).UnixMicro()
	require.NoError(t, h.db.WriteBatch(t.Context(), "boot", events, storage.BatchOptions{Snapshot: historyCoverage(4)}))
	q := url.Values{"from": {historyStart.Add(5 * time.Second).Format(time.RFC3339)}, "to": {historyStart.Add(15 * time.Second).Format(time.RFC3339)}}
	v, e := h.Timeseries(t.Context(), q)
	require.NoError(t, e)
	series := v.(historySeries)
	require.Len(t, series.Points, 1)
	assert.Equal(t, "1", series.Points[0].Outcomes["blocked"])
	assert.True(t, series.Complete)
	assert.Equal(t, historyStart.Add(5*time.Second), series.Points[0].Time)
	q.Set("to", historyStart.Add(time.Minute+11*time.Second).Format(time.RFC3339))
	v, e = h.Timeseries(t.Context(), q)
	require.NoError(t, e)
	series = v.(historySeries)
	require.Len(t, series.Points, 2)
	assert.Equal(t, "2", series.Points[0].Outcomes["blocked"])
	assert.Equal(t, "1", series.Points[1].Outcomes["cache"])
	// Longer custom windows need not land on hour boundaries either.
	q.Set("from", historyEnd.Add(-7*24*time.Hour+13*time.Minute).Format(time.RFC3339))
	q.Set("to", historyEnd.Add(13*time.Minute).Format(time.RFC3339))
	v, e = h.Timeseries(t.Context(), q)
	require.NoError(t, e)
	series = v.(historySeries)
	assert.Equal(t, int64(3600), series.ResolutionSeconds)
	assert.Len(t, series.Points, 169)
	assert.False(t, series.Complete)
}

func TestHistoryHTTPClientEnvelopeErrorsAndOmittedSettings(t *testing.T) {
	h, _ := historyFixture(t)
	require.NoError(t, h.db.WriteBatch(t.Context(), "boot", []stats.QueryEvent{historyEvent(1, stats.PolicyBlock)}))
	dir := t.TempDir()
	path := filepath.Join(dir, "dimsum.yaml")
	source := `# Keep operator comments.
version: 1
dns:
  listen: ["127.0.0.1:0"]
admin:
  listen: "127.0.0.1:0"
paths:
  data_dir: "./data"
  secrets_dir: "./secrets"
clients:
  - address: 192.0.2.10
    name: Office
`
	require.NoError(t, os.WriteFile(path, []byte(source), 0600))
	store, e := config.OpenStore(t.Context(), path, filepath.Join(dir, "state"), config.StoreOptions{Offline: true})
	require.NoError(t, e)
	handler := admin.New(control.New(control.Options{Store: store, ConfigPath: path, Provider: h}), admin.Options{}).LocalHandler()
	request := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	w := request(http.MethodGet, "/api/v1/clients", "")
	require.Equal(t, 200, w.Code, w.Body.String())
	var result struct {
		Items     []struct{ Address, Name string } `json:"items"`
		Observed  historyClients                   `json:"observed"`
		Available bool                             `json:"observed_available"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	assert.True(t, result.Available)
	require.Len(t, result.Items, 1)
	require.Len(t, result.Observed.Items, 1)
	assert.Equal(t, "Office", result.Items[0].Name)
	assert.Equal(t, "192.0.2.10", result.Observed.Items[0].Address)
	for _, path := range []string{"/api/v1/queries?limit=201", "/api/v1/summary?from=bad", "/api/v1/queries/0"} {
		w = request(http.MethodGet, path, "")
		assert.Equal(t, 400, w.Code)
		assert.Contains(t, w.Body.String(), `"code":"bad_request"`)
	}
	w = request(http.MethodGet, "/api/v1/queries/999999", "")
	assert.Equal(t, 404, w.Code)
	body := `{"revision":"` + store.Inspect().SavedRevision + `","edits":[{"path":["cache","max_stale_seconds"],"value":120}]}`
	w = request(http.MethodPatch, "/api/v1/settings", body)
	require.Equal(t, 200, w.Code, w.Body.String())
	assert.Equal(t, 120, store.Snapshot().Config().Cache.MaxStaleSeconds)
	saved, e := os.ReadFile(path)
	require.NoError(t, e)
	assert.True(t, strings.HasPrefix(string(saved), "# Keep operator comments.\n"))
}
