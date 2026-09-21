package resolve

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/richkeenan/dimsum/internal/config"
	"github.com/richkeenan/dimsum/internal/testutil"
	"github.com/richkeenan/dimsum/internal/upstream"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpstreamLeasesPinGenerationAndRetire(t *testing.T) {
	primary, err := testutil.NewUpstream(testutil.NewClock(time.Now()), func(testutil.Request) testutil.Response { return testutil.Response{Drop: true} })
	require.NoError(t, err)
	defer primary.Close()
	fallback, err := testutil.NewUpstream(testutil.NewClock(time.Now()), func(r testutil.Request) testutil.Response {
		p := append([]byte(nil), r.Wire...)
		p[2] |= 0x80
		return testutil.Response{Wire: p}
	})
	require.NoError(t, err)
	defer fallback.Close()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	text := []byte(fmt.Sprintf("version: 1\ndns:\n  listen: [127.0.0.1:0]\n  upstreams:\n    - %s\n  fallback_upstreams: [%s]\n  upstream_policy:\n    attempt_timeout_ms: 100\nadmin: {listen: '127.0.0.1:0'}\npaths: {data_dir: data, secrets_dir: secrets}\n", primary.Address(), fallback.Address()))
	require.NoError(t, os.WriteFile(path, text, 0600))
	ctx := context.Background()
	store, err := config.OpenStore(ctx, path, filepath.Join(dir, "state"), config.StoreOptions{Offline: true})
	require.NoError(t, err)
	pipeline := NewWithStore(nil, store)
	defer pipeline.Close()
	wire := []byte{0, 1, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 1, 'a', 0, 0, 1, 0, 1}
	type outcome struct {
		result upstream.ExchangeResult
		err    error
	}
	done := make(chan outcome, 1)
	old := store.Snapshot()
	go func() {
		r, e := pipeline.exchange(ctx, old, upstream.DefaultRoute, wire, make([]byte, 65535))
		done <- outcome{r, e}
	}()
	select {
	case <-primary.Requests():
	case <-time.After(time.Second):
		require.FailNow(t, "old request did not reach primary")
	}
	pipeline.upstreams.mu.Lock()
	retired := pipeline.upstreams.current
	pipeline.upstreams.mu.Unlock()
	d, err := config.Parse(text)
	require.NoError(t, err)
	d, err = d.Edit([]config.Edit{{Path: []string{"dns", "upstreams", "0"}, Value: fallback.Address()}})
	require.NoError(t, err)
	_, err = store.Save(ctx, old.Revision(), d)
	require.NoError(t, err)
	r, err := pipeline.exchange(ctx, store.Snapshot(), upstream.DefaultRoute, wire, make([]byte, 65535))
	require.NoError(t, err)
	assert.Equal(t, 1, r.Attempts)
	result := <-done
	require.NoError(t, result.err)
	assert.Equal(t, 2, result.result.Attempts)
	_, err = retired.client.Exchange(ctx, wire, make([]byte, 65535))
	assert.ErrorIs(t, err, net.ErrClosed)
	current := pipeline.upstreams.current
	_, err = store.Reload(ctx)
	require.NoError(t, err)
	_, err = pipeline.exchange(ctx, store.Snapshot(), upstream.DefaultRoute, wire, make([]byte, 65535))
	require.NoError(t, err)
	assert.Same(t, current, pipeline.upstreams.current, "unchanged upstream settings retain transport and health")
	require.NoError(t, pipeline.Close())
	_, err = current.client.Exchange(ctx, wire, make([]byte, 65535))
	assert.ErrorIs(t, err, net.ErrClosed)
}
