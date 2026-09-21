package config_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/richkeenan/dimsum/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCacheDefaultsAndBounds(t *testing.T) {
	for _, suffix := range []string{"", "cache:\n  stale_mode: off\n"} {
		d, err := config.Parse([]byte(sample + suffix))
		require.NoError(t, err)
		assert.Equal(t, 8<<20, d.Config().Cache.Bytes)
		assert.Equal(t, 4, d.Config().Cache.Shards)
		assert.Equal(t, 300, d.Config().Cache.MaxNegativeTTLSeconds)
		assert.Equal(t, 30, d.Config().Cache.StaleTTLSeconds)
	}
	for _, tc := range []struct {
		field    string
		min, max int
	}{
		{"bytes", 512 << 10, 1 << 30},
		{"shards", 1, 16},
		{"max_negative_ttl_seconds", 1, 86400},
		{"stale_ttl_seconds", 1, 300},
	} {
		for _, value := range []int{-1, 0, tc.min - 1, tc.min, tc.max, tc.max + 1} {
			t.Run(fmt.Sprintf("%s/%d", tc.field, value), func(t *testing.T) {
				_, err := config.Parse([]byte(sample + fmt.Sprintf("cache:\n  %s: %d\n", tc.field, value)))
				if value >= tc.min && value <= tc.max {
					assert.NoError(t, err)
				} else {
					require.Error(t, err)
					assert.Contains(t, err.Error(), "cache."+tc.field)
					assert.Contains(t, err.Error(), "line")
				}
			})
		}
	}
}

const cacheFields = "cache:\n  bytes: 8388608 # budget\n  shards: 4\n  max_negative_ttl_seconds: 300 # seconds\n  stale_ttl_seconds: 30 # response TTL\n  stale_mode: immediate\n  max_stale_seconds: 3600\n"

func TestCacheGroupedEditPreservesComments(t *testing.T) {
	d, err := config.Parse([]byte(sample + cacheFields))
	require.NoError(t, err)
	next, err := d.Edit([]config.Edit{
		{Path: []string{"cache", "bytes"}, Value: 16777216},
		{Path: []string{"cache", "shards"}, Value: 8},
		{Path: []string{"cache", "max_negative_ttl_seconds"}, Value: 120},
		{Path: []string{"cache", "stale_ttl_seconds"}, Value: 60},
	})
	require.NoError(t, err)
	assert.Equal(t, sample+"cache:\n  bytes: 16777216 # budget\n  shards: 8\n  max_negative_ttl_seconds: 120 # seconds\n  stale_ttl_seconds: 60 # response TTL\n  stale_mode: immediate\n  max_stale_seconds: 3600\n", string(next.Bytes()))
	assert.Equal(t, sample+cacheFields, string(d.Bytes()))
	_, err = d.Edit([]config.Edit{{Path: []string{"cache", "bytes"}, Value: 0}})
	assert.ErrorContains(t, err, "cache.bytes")
}

func TestCacheAllocationRequiresRestart(t *testing.T) {
	for _, tc := range []struct {
		field   string
		value   any
		restart bool
	}{
		{"bytes", 16 << 20, true},
		{"shards", 8, true},
		{"max_negative_ttl_seconds", 120, true},
		{"stale_ttl_seconds", 60, false},
		{"stale_mode", "off", false},
		{"max_stale_seconds", 60, false},
	} {
		t.Run(tc.field, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "dimsum.yaml")
			require.NoError(t, os.WriteFile(path, []byte(sample+cacheFields), 0600))
			store, err := config.OpenStore(ctx, path, path+".state", config.StoreOptions{})
			require.NoError(t, err)
			old := store.Snapshot()
			d, err := config.Parse([]byte(sample + cacheFields))
			require.NoError(t, err)
			d, err = d.Edit([]config.Edit{{Path: []string{"cache", tc.field}, Value: tc.value}})
			require.NoError(t, err)
			result, err := store.Save(ctx, old.Revision(), d)
			if !tc.restart {
				require.NoError(t, err)
				assert.False(t, result.RestartRequired)
				assert.Equal(t, d.Config().Cache, store.Snapshot().Config().Cache)
				return
			}
			require.ErrorContains(t, err, "requires explicit service restart")
			assert.True(t, result.RestartRequired)
			assert.Same(t, old, store.Snapshot())
			b, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, sample+cacheFields, string(b), "rejected save must not change disk")
			// External edits remain saved but inactive until an explicit restart.
			require.NoError(t, os.WriteFile(path, d.Bytes(), 0600))
			result, err = store.Reload(ctx)
			require.Error(t, err)
			assert.True(t, result.RestartRequired)
			assert.True(t, result.Pending)
			assert.Same(t, old, store.Snapshot())
			restarted, err := config.OpenStore(ctx, path, path+".state", config.StoreOptions{})
			require.NoError(t, err)
			assert.Equal(t, d.Config().Cache, restarted.Snapshot().Config().Cache)
			assert.False(t, restarted.Inspect().RestartRequired)
		})
	}
}
