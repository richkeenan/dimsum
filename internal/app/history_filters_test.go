package app

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/richkeenan/dimsum/internal/control"
	"github.com/richkeenan/dimsum/internal/policy"
	"github.com/richkeenan/dimsum/internal/stats"
	"github.com/richkeenan/dimsum/internal/storage"
	"github.com/richkeenan/dimsum/internal/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestArchivedHistoryFiltersAndCursorScope(t *testing.T) {
	h, _ := historyFixture(t)
	// No live configuration is present: source identity comes only from SQLite.
	for _, boot := range []string{"old", "new"} {
		for _, gen := range []uint32{7, 8} {
			events := []stats.QueryEvent{historyEvent(uint64(gen-7)*2+1, stats.PolicyBlock), historyEvent(uint64(gen-7)*2+2, stats.PolicyBlock)}
			for i := range events {
				events[i].Generation = gen
			}
			source := "deleted-feed"
			if boot == "new" {
				source = "replacement-feed"
			}
			require.NoError(t, h.db.WriteBatch(t.Context(), boot, events, storage.BatchOptions{Rules: []storage.RuleVersion{{Generation: gen, RuleID: 4, Description: "source: deliberately misleading", SourceID: source}}}))
		}
	}
	q := url.Values{"source_id": {"deleted-feed"}, "boot_id": {"old"}, "generation": {"7"}, "rule_id": {"4"}, "upstream_id": {"9"}, "limit": {"1"}}
	v, err := h.Queries(t.Context(), q)
	require.NoError(t, err)
	page := v.(historyQueries)
	require.Len(t, page.Items, 1)
	assert.Equal(t, "deleted-feed", page.Items[0].SourceID)
	require.NotEmpty(t, page.NextCursor)
	q.Set("cursor", page.NextCursor)
	h.now = func() time.Time { return historyEnd.Add(24 * time.Hour) }
	v, err = h.Queries(t.Context(), q)
	require.NoError(t, err)
	next := v.(historyQueries)
	require.Len(t, next.Items, 1)
	assert.Equal(t, page.Range, next.Range)
	assert.NotEqual(t, page.Items[0].ID, next.Items[0].ID)
	for key, changed := range map[string]string{"source_id": "replacement-feed", "boot_id": "new", "generation": "8", "rule_id": "5", "upstream_id": "10"} {
		original := q.Get(key)
		q.Set(key, changed)
		_, err = h.Queries(t.Context(), q)
		assert.ErrorIs(t, err, control.BadRequest, key)
		q.Set(key, original)
	}
	q.Del("cursor")
	q.Set("from", historyStart.Format(time.RFC3339))
	q.Set("to", historyEnd.Format(time.RFC3339))
	q.Set("boot_id", "new")
	v, err = h.Queries(t.Context(), q)
	require.NoError(t, err)
	assert.Empty(t, v.(historyQueries).Items, "same numeric rule in a different boot has a different archived source")
	q = url.Values{"from": {historyStart.Format(time.RFC3339)}, "to": {historyEnd.Format(time.RFC3339)}, "source_id": {"deleted-feed"}}
	v, err = h.Queries(t.Context(), q)
	require.NoError(t, err)
	assert.Len(t, v.(historyQueries).Items, 4)
}

func TestHistoryFilterValidation(t *testing.T) {
	h, _ := historyFixture(t)
	for _, raw := range []string{"rule_id=4", "upstream_id=9", "rule_id=4&boot_id=old", "source_id=", "boot_id=", "generation=-1", "generation=4294967296", "rule_id=01&boot_id=old&generation=7", "upstream_id=65536&boot_id=old&generation=7", "source=feed", "source_id=a&source_id=b", "source_id=" + strings.Repeat("x", 513), "boot_id=" + strings.Repeat("x", 129)} {
		q, err := url.ParseQuery(raw)
		require.NoError(t, err)
		_, err = h.Queries(t.Context(), q)
		assert.ErrorIs(t, err, control.BadRequest, raw)
	}
}

func TestSourceMetadataNeverTruncatesExactIdentity(t *testing.T) {
	o := &observability{}
	for i, source := range []string{strings.Repeat("x", 512), strings.Repeat("x", 513)} {
		id := uint32(i + 1)
		o.noteRule(7, id, transport.Result{Rule: policy.Rule{SourceID: source}})
		metadata, err := o.enrich([]stats.QueryEvent{{Generation: 7, RuleID: id}})
		require.NoError(t, err)
		require.Len(t, metadata.Rules, 1)
		if len(source) == 512 {
			assert.Equal(t, source, metadata.Rules[0].SourceID)
		} else {
			assert.Empty(t, metadata.Rules[0].SourceID)
		}
	}
}
