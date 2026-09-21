package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCLIValidateInspectsCacheSettings(t *testing.T) {
	base, err := os.ReadFile("../../testdata/config/dimsum.yaml")
	require.NoError(t, err)
	for _, tc := range []struct {
		name, cache                string
		bytes, shards, negativeTTL int
	}{
		{"defaults", "", 8 << 20, 4, 300},
		{"explicit", "  bytes: 524288\n  shards: 16\n  max_negative_ttl_seconds: 86400\n", 512 << 10, 16, 86400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "dimsum.yaml")
			require.NoError(t, os.WriteFile(path, append(bytes.Clone(base), []byte(tc.cache)...), 0600))
			var out, stderr bytes.Buffer
			require.Zero(t, run(context.Background(), []string{"validate", "-config", path}, &out, &stderr), stderr.String())
			var result struct {
				Cache           map[string]any `json:"cache"`
				ActiveAvailable bool           `json:"active_available"`
			}
			require.NoError(t, json.Unmarshal(out.Bytes(), &result))
			assert.EqualValues(t, tc.bytes, result.Cache["bytes"])
			assert.EqualValues(t, tc.shards, result.Cache["shards"])
			assert.EqualValues(t, tc.negativeTTL, result.Cache["max_negative_ttl_seconds"])
			assert.False(t, result.ActiveAvailable)
		})
	}
}
