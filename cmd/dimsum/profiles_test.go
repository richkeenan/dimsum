package main

import (
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type failedProfileWriter struct{ err error }

func (w failedProfileWriter) Write([]byte) (int, error) { return 0, w.err }

func TestCPUProfileReportsWriteFailure(t *testing.T) {
	failure := errors.New("profile destination full")
	stop, err := startCPUProfile(failedProfileWriter{failure})
	require.NoError(t, err)
	assert.ErrorIs(t, stop(), failure)
}

func TestProfilesWriteOnShutdown(t *testing.T) {
	dir := t.TempDir()
	cpu, heap := filepath.Join(dir, "cpu.pprof"), filepath.Join(dir, "heap.pprof")
	stop, err := startProfiles(cpu, heap)
	require.NoError(t, err)
	require.NoError(t, stop())
	for _, path := range []string{cpu, heap} {
		file, err := os.Open(path)
		require.NoError(t, err)
		info, err := file.Stat()
		require.NoError(t, err)
		assert.EqualValues(t, 0600, info.Mode().Perm())
		r, err := gzip.NewReader(file)
		require.NoError(t, err)
		data, err := io.ReadAll(r)
		require.NoError(t, err)
		assert.NotEmpty(t, data, "profile must contain a protobuf payload")
		assert.NoError(t, r.Close())
		assert.NoError(t, file.Close())
	}
}

func TestProfilesPreserveExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "existing")
	require.NoError(t, os.WriteFile(path, []byte("keep"), 0600))
	_, err := startProfiles(path, "")
	require.Error(t, err)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "keep", string(data))
}

func TestProfilesDisabledAndOpenFailure(t *testing.T) {
	stop, err := startProfiles("", "")
	require.NoError(t, err)
	assert.NoError(t, stop())
	_, err = startProfiles("", filepath.Join(t.TempDir(), "absent", "heap.pprof"))
	assert.Error(t, err)
}
