package storagebench

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/richkeenan/dimsum/internal/stats"
	"github.com/richkeenan/dimsum/internal/storage"
)

// Use a fixed iteration count for reproducible retained cardinality. This is a
// synthetic local-filesystem benchmark, not a native Pi driver comparison.
func BenchmarkWriteBatch512(b *testing.B) {
	path := filepath.Join(b.TempDir(), "history.sqlite")
	d, err := storage.Open(path)
	if err != nil {
		b.Fatal(err)
	}
	defer d.Close()
	events := make([]stats.QueryEvent, 512)
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC).UnixMicro()
	for i := range events {
		events[i] = stats.QueryEvent{Timestamp: start, Outcome: stats.FreshCache, QType: 1, QClass: 1, QNameLength: 4, Duration: 500}
		copy(events[i].QName[:], []byte{2, byte(i), byte(i >> 8), 0})
		events[i].Client[15] = byte(i % 32)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for j := range events {
			events[j].Sequence = uint64(i*512 + j + 1)
		}
		if err := d.WriteBatch(context.Background(), "bench", events); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	cp, err := d.Checkpoint(context.Background())
	if err != nil {
		b.Fatal(err)
	}
	b.ReportMetric(float64(cp.AllocatedPages*cp.PageSize)/float64(b.N*512), "db-bytes/event")
	wal, err := os.Stat(path + "-wal")
	if err != nil {
		b.Fatal(err)
	}
	b.ReportMetric(float64(wal.Size()), "wal-bytes")
}

func BenchmarkHistoryPage(b *testing.B) {
	d, err := storage.Open(filepath.Join(b.TempDir(), "history.sqlite"))
	if err != nil {
		b.Fatal(err)
	}
	defer d.Close()
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	events := make([]stats.QueryEvent, 512)
	for i := range events {
		events[i] = stats.QueryEvent{Timestamp: start.UnixMicro(), Outcome: stats.PolicyBlock, QNameLength: 1}
	}
	for i := range 8 {
		for j := range events {
			events[j].Sequence = uint64(i*512 + j + 1)
		}
		if err = d.WriteBatch(context.Background(), "bench", events); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p, err := d.Query(context.Background(), storage.QueryOptions{Start: start, End: start.Add(time.Hour), Limit: 100})
		if err != nil || len(p.Rows) != 100 {
			b.Fatalf("query: %v, rows %d", err, len(p.Rows))
		}
	}
}
