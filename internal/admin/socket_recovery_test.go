package admin

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSocketRecovery(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0700))
	path := filepath.Join(dir, "control.sock")
	old, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	require.NoError(t, err)
	old.SetUnlinkOnClose(false)
	require.NoError(t, old.Close())
	s := New(nil, Options{})
	l, err := s.ListenUnix(path)
	require.NoError(t, err)
	defer l.Close()
	_, err = s.ListenUnix(path)
	assert.Error(t, err)
	require.NoError(t, l.Close())
	l, err = s.ListenUnix(path)
	require.NoError(t, err)
	require.NoError(t, l.Close())
	require.NoError(t, os.WriteFile(path, []byte("keep"), 0600))
	_, err = s.ListenUnix(path)
	assert.Error(t, err)
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "keep", string(b))
}
