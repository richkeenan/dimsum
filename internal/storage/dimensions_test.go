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

func TestBatchExistingDimensionsAvoidInsertAttempts(t *testing.T) {
	d := openTest(t)
	ctx := context.Background()
	require.NoError(t, d.WriteBatch(ctx, "existing", []stats.QueryEvent{event(1)}))
	_, err := d.write.Exec(`CREATE TABLE existing_attempts(kind TEXT);
CREATE TRIGGER existing_domain BEFORE INSERT ON domains BEGIN INSERT INTO existing_attempts VALUES('domain'); END;
CREATE TRIGGER existing_client BEFORE INSERT ON clients BEGIN INSERT INTO existing_attempts VALUES('client'); END;`)
	require.NoError(t, err)
	require.NoError(t, d.WriteBatch(ctx, "existing", []stats.QueryEvent{event(2), event(3)}))
	var attempts int
	require.NoError(t, d.read.QueryRow("SELECT COUNT(*) FROM existing_attempts").Scan(&attempts))
	assert.Zero(t, attempts, "existing dictionary entries should be read without invoking the insertion path")
	page, err := d.Query(ctx, QueryOptions{Start: testStart, End: testStart.Add(time.Hour)})
	require.NoError(t, err)
	require.Len(t, page.Rows, 3)
	for _, row := range page.Rows {
		assert.Equal(t, event(1).QName, row.Event.QName)
		assert.Equal(t, event(1).Client, row.Event.Client)
	}
}

func TestBatchCombinesAggregateUpdatesWithoutLosingEvents(t *testing.T) {
	d := openTest(t)
	_, err := d.write.Exec(`CREATE TABLE aggregate_attempts(kind TEXT);
CREATE TRIGGER rollup_attempt BEFORE INSERT ON rollups BEGIN INSERT INTO aggregate_attempts VALUES('rollup'); END;
CREATE TRIGGER ranking_attempt BEFORE INSERT ON rankings_hour BEGIN INSERT INTO aggregate_attempts VALUES('ranking'); END;`)
	require.NoError(t, err)
	events := make([]stats.QueryEvent, 6)
	for i := range events {
		events[i] = event(uint64(i + 1))
	}
	events[4].Outcome = stats.FreshCache
	events[5].Outcome = stats.AdmissionRejected
	ctx := context.Background()
	require.NoError(t, d.WriteBatch(ctx, "aggregates", events))
	var rollups, rankings int
	require.NoError(t, d.write.QueryRow("SELECT COUNT(*) FROM aggregate_attempts WHERE kind='rollup'").Scan(&rollups))
	require.NoError(t, d.write.QueryRow("SELECT COUNT(*) FROM aggregate_attempts WHERE kind='ranking'").Scan(&rankings))
	assert.Equal(t, 9, rollups, "three outcomes at three resolutions require nine combined updates")
	assert.Equal(t, 2, rankings, "one client and one blocked domain require two combined updates")
	var count, duration, histogram int
	require.NoError(t, d.write.QueryRow("SELECT count,duration,h2 FROM rollups WHERE resolution=60 AND outcome=?", stats.PolicyBlock).Scan(&count, &duration, &histogram))
	assert.Equal(t, 4, count)
	assert.Equal(t, 2000, duration)
	assert.Equal(t, 4, histogram)
	rank, err := d.Rankings(ctx, testStart, testStart.Add(time.Hour))
	require.NoError(t, err)
	require.Len(t, rank.Clients, 1)
	require.Len(t, rank.BlockedDomains, 1)
	assert.EqualValues(t, 5, rank.Clients[0].Count, "admission rejection is not a ranked client query")
	assert.EqualValues(t, 4, rank.BlockedDomains[0].Count)
	page, err := d.Query(ctx, QueryOptions{Start: testStart, End: testStart.Add(time.Hour)})
	require.NoError(t, err)
	assert.Len(t, page.Rows, 6, "aggregation must not discard detail rows")
	// Replaying a committed batch must not double either details or aggregates.
	require.NoError(t, d.WriteBatch(ctx, "aggregates", events))
	require.NoError(t, d.write.QueryRow("SELECT count FROM rollups WHERE resolution=60 AND outcome=?", stats.PolicyBlock).Scan(&count))
	assert.Equal(t, 4, count)
	for i := range events {
		events[i].Sequence += 6
	}
	require.NoError(t, d.WriteBatch(ctx, "aggregates", events))
	rank, err = d.Rankings(ctx, testStart, testStart.Add(time.Hour))
	require.NoError(t, err)
	require.Len(t, rank.Clients, 1)
	require.Len(t, rank.BlockedDomains, 1)
	assert.EqualValues(t, 10, rank.Clients[0].Count, "conflicts must add the whole aggregate, not one")
	assert.EqualValues(t, 8, rank.BlockedDomains[0].Count)
	require.NoError(t, d.write.QueryRow("SELECT count,duration,h2 FROM rollups WHERE resolution=60 AND outcome=?", stats.PolicyBlock).Scan(&count, &duration, &histogram))
	assert.Equal(t, 8, count)
	assert.Equal(t, 4000, duration)
	assert.Equal(t, 8, histogram)
}

func TestAggregateResultsIndependentOfBatchPartition(t *testing.T) {
	whole, individual := openTest(t), openTest(t)
	ctx := context.Background()
	events := make([]stats.QueryEvent, 24)
	durations := []uint32{0, 1, 500, 1000, 10000, ^uint32(0)}
	for i := range events {
		events[i] = event(uint64(i + 1))
		events[i].Timestamp = testStart.Add(time.Duration(i/4) * 13 * time.Hour).UnixMicro()
		events[i].Outcome = stats.Outcome(i % int(stats.OutcomeCount))
		events[i].Duration = durations[i%len(durations)]
		events[i].Client[15] = byte(i % 3)
		events[i].QName[1] = byte('a' + i%4)
	}
	require.NoError(t, whole.WriteBatch(ctx, "partition", events))
	for _, e := range events {
		require.NoError(t, individual.WriteBatch(ctx, "partition", []stats.QueryEvent{e}))
	}
	end := testStart.Add(4 * 24 * time.Hour)
	wantSummary, err := individual.Summary(ctx, testStart, end)
	require.NoError(t, err)
	gotSummary, err := whole.Summary(ctx, testStart, end)
	require.NoError(t, err)
	assert.Equal(t, wantSummary, gotSummary)
	for _, resolution := range []time.Duration{time.Minute, time.Hour, 24 * time.Hour} {
		seriesEnd := end
		if resolution == time.Minute {
			seriesEnd = testStart.Add(24 * time.Hour)
		}
		want, err := individual.Timeseries(ctx, testStart, seriesEnd, resolution)
		require.NoError(t, err)
		got, err := whole.Timeseries(ctx, testStart, seriesEnd, resolution)
		require.NoError(t, err)
		assert.Equal(t, want, got)
	}
	wantRank, err := individual.Rankings(ctx, testStart, end)
	require.NoError(t, err)
	gotRank, err := whole.Rankings(ctx, testStart, end)
	require.NoError(t, err)
	assert.Equal(t, wantRank, gotRank)
}
