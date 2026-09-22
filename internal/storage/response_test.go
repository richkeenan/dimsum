package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/richkeenan/dimsum/internal/queryresult"
	"github.com/richkeenan/dimsum/internal/stats"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResponseHistorySurvivesReopenAndDistinguishesUnrecorded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.sqlite")
	d, err := Open(path)
	require.NoError(t, err)
	ctx := context.Background()
	response := &queryresult.Summary{Records: []queryresult.Record{{Name: "a", Type: "A", Value: "192.0.2.7", TTL: 42, Section: "answer"}}, Truncated: true}
	err = d.WriteBatch(ctx, "boot", []stats.QueryEvent{event(1), event(2), event(3)}, BatchOptions{Responses: map[uint64]*queryresult.Summary{2: response, 3: {Records: []queryresult.Record{}}}})
	require.NoError(t, err)
	require.NoError(t, d.Close())
	d, err = Open(path)
	require.NoError(t, err)
	defer d.Close()
	p, err := d.Query(ctx, QueryOptions{Start: testStart, End: testStart.Add(time.Hour)})
	require.NoError(t, err)
	require.Len(t, p.Rows, 3)
	assert.Nil(t, p.Rows[2].Response)
	assert.Equal(t, response, p.Rows[1].Response)
	require.NotNil(t, p.Rows[0].Response)
	assert.Empty(t, p.Rows[0].Response.Records)
	for _, row := range p.Rows {
		detail, err := d.QueryByID(ctx, row.ID)
		require.NoError(t, err)
		assert.Equal(t, row.Response, detail.Response)
	}
}
