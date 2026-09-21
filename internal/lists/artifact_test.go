package lists

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestArtifactInterruptedReplacement(t *testing.T) {
	if path := os.Getenv("DIMSUM_ARTIFACT_CRASH_CHILD"); path != "" {
		payload := bytes.Repeat([]byte("new version\n"), 1<<20)
		for range 100 {
			if WriteArtifact(path, payload) != nil {
				os.Exit(3)
			}
		}
		os.Exit(0)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "active")
	require.NoError(t, WriteArtifact(path, []byte("old version")))
	child := exec.Command(os.Args[0], "-test.run=^TestArtifactInterruptedReplacement$")
	child.Env = append(os.Environ(), "DIMSUM_ARTIFACT_CRASH_CHILD="+path)
	require.NoError(t, child.Start())
	defer func() { _ = child.Process.Kill(); _ = child.Wait() }()
	require.Eventually(t, func() bool {
		matches, _ := filepath.Glob(filepath.Join(dir, ".dimsum-artifact-*"))
		return len(matches) > 0
	}, 5*time.Second, time.Millisecond)
	require.NoError(t, child.Process.Kill())
	_ = child.Wait()
	payload, err := ReadArtifact(path, 16<<20)
	require.NoError(t, err)
	assert.True(t, bytes.Equal(payload, []byte("old version")) || bytes.Equal(payload, bytes.Repeat([]byte("new version\n"), 1<<20)), "interrupted replacement must be old or complete new")
}
