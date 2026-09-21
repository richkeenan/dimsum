package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCLIValidationAndUsage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dimsum.yaml")
	require.NoError(t, os.WriteFile(path, []byte("version: ["), 0600))
	for _, args := range [][]string{{"validate", "-config", path}, {"serve", "-config", path}, {"unknown"}, {"validate", "extra"}} {
		var out, stderr bytes.Buffer
		assert.NotZero(t, run(context.Background(), args, &out, &stderr), "accepted %v", args)
		assert.NotEmpty(t, stderr.String(), "missing diagnostic")
	}
	var out, stderr bytes.Buffer
	assert.Zero(t, run(context.Background(), []string{"--help"}, &out, &stderr))
	assert.Contains(t, out.String(), "validate", "help not discoverable")
	assert.Zero(t, run(context.Background(), []string{"validate", "-config", "../../testdata/config/dimsum.yaml"}, &out, &stderr), stderr.String())
	assert.Contains(t, out.String(), `"active_available":false`, "offline validation pretends to know active state")
}
