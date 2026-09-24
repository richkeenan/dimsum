package app

import (
	"testing"

	"github.com/miekg/dns"
	"github.com/richkeenan/dimsum/internal/stats"
	"github.com/richkeenan/dimsum/internal/storage"
	"github.com/richkeenan/dimsum/internal/transport"
	"github.com/stretchr/testify/require"
)

func BenchmarkEnrichSmallResponse(b *testing.B) {
	o := &observability{collector: stats.New(1)}
	m := new(dns.Msg)
	m.SetQuestion("small.example.", dns.TypeA)
	m.Response = true
	rr, err := dns.NewRR("small.example. 60 IN A 192.0.2.8")
	require.NoError(b, err)
	m.Answer = []dns.RR{rr}
	wire, err := m.Pack()
	require.NoError(b, err)
	o.observe(nil, transport.Result{Admitted: true, Outcome: transport.FreshAnswer, Response: wire})
	e := <-o.collector.Events()
	events := []stats.QueryEvent{e}
	var result storage.BatchOptions
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		result, err = o.enrich(events)
	}
	b.StopTimer()
	require.NoError(b, err)
	require.NotNil(b, result.Responses[e.Sequence])
	require.Len(b, result.Responses[e.Sequence].Records, 1)
	require.Equal(b, "192.0.2.8", result.Responses[e.Sequence].Records[0].Value)
}

func TestSmallResponseEnrichmentByteBudget(t *testing.T) {
	result := testing.Benchmark(BenchmarkEnrichSmallResponse)
	require.Positive(t, result.N, "the benchmark must complete its correctness checks")
	// A small owned DNS reply plus its formatted record must not allocate full
	// 4 KiB capture slots, especially not once for storage and again for parsing.
	require.LessOrEqual(t, result.AllocedBytesPerOp(), int64(2048))
}
