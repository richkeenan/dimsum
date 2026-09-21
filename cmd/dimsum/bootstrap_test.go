package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/richkeenan/dimsum/internal/admin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBootstrapOneTimeSecret(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	password := filepath.Join(dir, "password")
	require.NoError(t, os.WriteFile(path, []byte("version: 1\ndns:\n  listen: [127.0.0.1:5353]\nadmin:\n  listen: 127.0.0.1:8080\npaths:\n  data_dir: data\n  secrets_dir: secrets\n"), 0600))
	require.NoError(t, os.WriteFile(password, []byte("test-password-at-least-twelve\n"), 0600))
	var out, stderr bytes.Buffer
	args := []string{"bootstrap", "-config", path, "-password-file", password}
	require.Zero(t, run(context.Background(), args, &out, &stderr), stderr.String())
	hash, err := admin.LoadPasswordHash(filepath.Join(dir, "secrets", "admin.hash"))
	require.NoError(t, err)
	assert.Contains(t, hash, "pbkdf2-sha256$")
	assert.Equal(t, 1, run(context.Background(), args, &out, &stderr))
	unchanged, err := admin.LoadPasswordHash(filepath.Join(dir, "secrets", "admin.hash"))
	require.NoError(t, err)
	assert.Equal(t, hash, unchanged)
}
