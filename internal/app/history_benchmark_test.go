package app

import (
	"context"
	"net/url"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/richkeenan/dimsum/internal/stats"
	"github.com/richkeenan/dimsum/internal/storage"
	"github.com/stretchr/testify/require"
)

// Synthetic seven-day workload, roughly the observed deployment's event count.
// No live history or device inventory is used. Run with -benchmem -count=3.
func BenchmarkOverviewHistory(b *testing.B) {
	db, err := storage.Open(filepath.Join(b.TempDir(), "history.sqlite"))
	require.NoError(b, err)
	b.Cleanup(func() { require.NoError(b, db.Close()) })
	ctx := context.Background()
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	from := start.Add(57*time.Second + 542*time.Millisecond)
	to := from.Add(7 * 24 * time.Hour)
	var firstTimestamp, lastTimestamp int64
	inWindow := 0
	const total = 120000
	for first := 0; first < total; first += 500 {
		events := make([]stats.QueryEvent, 500)
		for j := range events {
			i := first + j
			e := historyEvent(uint64(i+1), stats.Outcome(i%6))
			e.Timestamp = start.Add(time.Duration(i) * (7 * 24 * time.Hour / total)).UnixMicro()
			if i == 0 {
				firstTimestamp = e.Timestamp
			}
			lastTimestamp = e.Timestamp
			if e.Timestamp >= from.UnixMicro() && e.Timestamp < to.UnixMicro() {
				inWindow++
			}
			e.Client[15] = byte(i%40 + 1)
			e.Duration = uint32(100 + i%10000)
			e.QName[1], e.QName[2] = byte('a'+i%26), byte('a'+i/26%26)
			events[j] = e
		}
		require.NoError(b, db.WriteBatch(ctx, "bench", events))
	}
	require.Equal(b, start.UnixMicro(), firstTimestamp)
	require.Equal(b, start.Add(7*24*time.Hour-5040*time.Millisecond).UnixMicro(), lastTimestamp)
	require.Equal(b, 119988, inWindow)
	h := NewHistoryProvider(db, nil).(*historyProvider)
	q := url.Values{"from": {from.Format(time.RFC3339Nano)}, "to": {to.Format(time.RFC3339Nano)}}
	type read func(context.Context, url.Values) (any, error)
	for _, tc := range []struct {
		name string
		read read
	}{{"summary", h.Summary}, {"rankings", h.Rankings}} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_, err := tc.read(ctx, q)
				require.NoError(b, err)
			}
		})
	}
	b.Run("concurrent-overview", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			reads := []read{h.Summary, h.Rankings, h.Timeseries, h.Performance}
			errs := make([]error, len(reads))
			var wg sync.WaitGroup
			for i, read := range reads {
				wg.Go(func() { _, errs[i] = read(ctx, q) })
			}
			wg.Wait()
			for _, err := range errs {
				require.NoError(b, err)
			}
		}
	})
}
