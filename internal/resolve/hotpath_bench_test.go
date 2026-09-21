package resolve

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/richkeenan/dimsum/internal/config"
	"github.com/richkeenan/dimsum/internal/dnswire"
	"github.com/richkeenan/dimsum/internal/transport"
	"github.com/stretchr/testify/require"
)

// Measures the warmed resolver path, including wire-name conversion, cache
// lookup, TTL update and reply personalization. Socket I/O and stats are excluded.
func BenchmarkWarmPipeline(b *testing.B) {
	p, r, out := warmPipeline(b)
	var n int
	var err error
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		n, err = p.Resolve(context.Background(), r, out)
		if err != nil {
			break
		}
	}
	b.StopTimer()
	require.NoError(b, err)
	require.Greater(b, n, 12)
	require.Equal(b, transport.FreshAnswer, r.Result.Outcome)
}

func warmPipeline(b testing.TB, managed ...bool) (*Pipeline, *transport.Request, []byte) {
	p := New(nil)
	b.Cleanup(func() { p.Close() })
	var snapshot *config.Snapshot
	if len(managed) > 0 && managed[0] {
		path := filepath.Join(b.TempDir(), "config.yaml")
		require.NoError(b, os.WriteFile(path, []byte("version: 1\ndns:\n  listen: [127.0.0.1:5353]\n  upstreams: [127.0.0.1:9]\nadmin:\n  listen: 127.0.0.1:8080\npaths:\n  data_dir: data\n  secrets_dir: secrets\nrules:\n  - id: other\n    action: deny\n    kind: exact\n    pattern: unrelated.test\n    enabled: true\n"), 0600))
		store, err := config.OpenStore(context.Background(), path, path+".state", config.StoreOptions{Offline: true})
		require.NoError(b, err)
		p.store = store
		snapshot = store.Snapshot()
	}
	c, _, err := p.cacheFor(snapshot)
	require.NoError(b, err)
	r := transport.Request{Wire: []byte{0, 1, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 3, 'w', 'w', 'w', 7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 4, 't', 'e', 's', 't', 0, 0, 1, 0, 1}}
	require.NoError(b, dnswire.ParseRequest(r.Wire, &r.Message))
	out := make([]byte, 1232)
	n, err := dnswire.BuildReply(out, &r.Message, dnswire.Reply{Null: true, TTL: 3600, RecursionAvailable: true}, 1232)
	require.NoError(b, err)
	key, ok := cacheKey(&r, snapshot)
	require.True(b, ok)
	require.True(b, c.Put(key, out[:n], time.Now()))
	return p, &r, out
}

// Includes immutable configuration, local-zone routing, pre/post name policy
// and cache reconstruction, but excludes sockets, client-name discovery and stats.
func BenchmarkWarmManagedPipeline(b *testing.B) {
	p, r, out := warmPipeline(b, true)
	var n int
	var err error
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		n, err = p.Resolve(context.Background(), r, out)
		if err != nil {
			break
		}
	}
	b.StopTimer()
	require.NoError(b, err)
	require.Greater(b, n, 12)
	require.Equal(b, transport.FreshAnswer, r.Result.Outcome)
}

func TestWarmPipelineAllocationBudget(t *testing.T) {
	for _, managed := range []bool{false, true} {
		p, r, out := warmPipeline(t, managed)
		var n int
		var err error
		allocs := testing.AllocsPerRun(100, func() { n, err = p.Resolve(context.Background(), r, out) })
		require.NoError(t, err)
		require.Greater(t, n, 12)
		require.LessOrEqual(t, allocs, float64(1), "only the owned policy-name string may allocate")
	}
}
