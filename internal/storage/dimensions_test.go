package storage

import (
	"context"
	"testing"
	"time"

	"github.com/richkeenan/dimsum/internal/stats"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBatchResolvesRepeatedDimensionsOnce(t *testing.T) {
	d := openTest(t)
	_, err := d.write.Exec(`CREATE TABLE dimension_attempts(kind TEXT);
CREATE TRIGGER domain_attempt BEFORE INSERT ON domains BEGIN INSERT INTO dimension_attempts VALUES('domain'); END;
CREATE TRIGGER client_attempt BEFORE INSERT ON clients BEGIN INSERT INTO dimension_attempts VALUES('client'); END;`)
	require.NoError(t, err)
	events := make([]stats.QueryEvent, 6)
	for i := range events {
		events[i] = event(uint64(i + 1))
		events[i].QName[1] = []byte{'a', 'b', 'c', 'a', 'b', 'c'}[i]
		events[i].Client[15] = byte(i % 2)
	}
	require.NoError(t, d.WriteBatch(context.Background(), "dimensions", events))
	var domains, clients int
	require.NoError(t, d.write.QueryRow("SELECT COUNT(*) FROM dimension_attempts WHERE kind='domain'").Scan(&domains))
	require.NoError(t, d.write.QueryRow("SELECT COUNT(*) FROM dimension_attempts WHERE kind='client'").Scan(&clients))
	assert.Equal(t, 3, domains, "repeated names must not issue repeated INSERT attempts")
	assert.Equal(t, 2, clients, "repeated clients must not issue repeated INSERT attempts")
	page, err := d.Query(context.Background(), QueryOptions{Start: testStart, End: testStart.Add(time.Hour)})
	require.NoError(t, err)
	require.Len(t, page.Rows, len(events))
	for i, row := range page.Rows {
		want := events[len(events)-1-i]
		assert.Equal(t, want.QName, row.Event.QName)
		assert.Equal(t, want.Client, row.Event.Client)
	}
}
