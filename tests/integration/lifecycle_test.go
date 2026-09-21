package integration_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/richkeenan/dimsum/internal/app"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLifecycleMalformedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dimsum.yaml")
	require.NoError(t, os.WriteFile(path, []byte("version: ["), 0600))
	s := new(app.Service)
	t.Cleanup(func() { s.Close() })
	require.Error(t, s.StartFile(context.Background(), path), "malformed file started service")
	a := s.Addresses()
	assert.Empty(t, a.DNS, "partial start")
	assert.Empty(t, a.Admin, "partial start")
}
