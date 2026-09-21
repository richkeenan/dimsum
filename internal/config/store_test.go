package config

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/richkeenan/dimsum/internal/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fixtureStore(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(p, []byte(storeFixture), 0600))
	return p, filepath.Join(dir, "state")
}
func denyDoc(t *testing.T) *Document {
	t.Helper()
	d, e := Parse([]byte(storeFixture))
	require.NoError(t, e)
	d, e = d.Append([]string{"rules"}, CustomRule{ID: "deny", Action: "deny", Kind: policy.Exact, Pattern: "ads.test", Enabled: true})
	require.NoError(t, e)
	return d
}

func TestStoreSaveInvalidConflictRecovery(t *testing.T) {
	p, state := fixtureStore(t)
	ctx := context.Background()
	s, err := OpenStore(ctx, p, state, StoreOptions{})
	require.NoError(t, err)
	original := s.Inspect()
	result, err := s.Save(ctx, original.SavedRevision, denyDoc(t))
	require.NoError(t, err)
	assert.Greater(t, result.ActiveGeneration, original.ActiveGeneration)
	n, err := policy.NormalizeName("ads.test")
	require.NoError(t, err)
	assert.Equal(t, policy.Block, s.Snapshot().Policy().Match(n).Result)
	require.NoError(t, os.WriteFile(p, []byte("version: [invalid"), 0600))
	_, err = s.Save(ctx, result.SavedRevision, denyDoc(t))
	assert.ErrorIs(t, err, ErrConflict)
	_, err = s.Reload(ctx)
	require.Error(t, err)
	inspect := s.Inspect()
	assert.NotEqual(t, inspect.SavedRevision, inspect.ActiveRevision)
	assert.Contains(t, inspect.Error, p)
	restarted, err := OpenStore(ctx, p, state, StoreOptions{Offline: true})
	require.NoError(t, err)
	assert.True(t, restarted.Inspect().Recovered)
	assert.Equal(t, policy.Block, restarted.Snapshot().Policy().Match(n).Result)
	// Interrupted stage files cannot affect the single recovery manifest.
	require.NoError(t, os.WriteFile(filepath.Join(state, ".interrupted"), []byte("partial"), 0600))
	restarted, err = OpenStore(ctx, p, state, StoreOptions{Offline: true})
	require.NoError(t, err)
	assert.Equal(t, result.ActiveGeneration, restarted.Inspect().ActiveGeneration)
}

func TestWatchWritesRenamesAndCorrection(t *testing.T) {
	p, state := fixtureStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, err := OpenStore(ctx, p, state, StoreOptions{PollInterval: 5 * time.Millisecond})
	require.NoError(t, err)
	done := make(chan struct{})
	go func() { s.Watch(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	require.NoError(t, os.WriteFile(p, []byte("invalid: ["), 0600))
	require.Eventually(t, func() bool { return s.Inspect().Error != "" }, time.Second, 5*time.Millisecond)
	d := denyDoc(t)
	tmp := p + ".tmp"
	require.NoError(t, os.WriteFile(tmp, d.Bytes(), 0600))
	require.NoError(t, os.Rename(tmp, p))
	require.Eventually(t, func() bool { return s.Inspect().Error == "" && s.Inspect().ActiveRevision == d.Revision() }, time.Second, 5*time.Millisecond)
}

func TestPublicationFailureKeepsSnapshot(t *testing.T) {
	p, state := fixtureStore(t)
	s, err := OpenStore(context.Background(), p, state, StoreOptions{})
	require.NoError(t, err)
	old := s.Snapshot()
	require.NoError(t, os.Remove(filepath.Join(state, "active.artifact")))
	require.NoError(t, os.Mkdir(filepath.Join(state, "active.artifact"), 0700))
	_, err = s.Save(context.Background(), s.Inspect().SavedRevision, denyDoc(t))
	require.Error(t, err)
	assert.Same(t, old, s.Snapshot())
	assert.NotEqual(t, s.Inspect().SavedRevision, s.Inspect().ActiveRevision)
}

func TestGroupedStageAndRestartRequirement(t *testing.T) {
	p, state := fixtureStore(t)
	ctx := context.Background()
	s, err := OpenStore(ctx, p, state, StoreOptions{})
	require.NoError(t, err)
	d := denyDoc(t)
	d, err = d.Edit([]Edit{{Path: []string{"cache", "max_stale_seconds"}, Value: 90}})
	require.NoError(t, err)
	stage, err := s.Stage(s.Inspect().SavedRevision, d)
	require.NoError(t, err)
	assert.NotEqual(t, d.Revision(), s.Inspect().SavedRevision)
	s, err = OpenStore(ctx, p, state, StoreOptions{})
	require.NoError(t, err)
	result, err := s.CommitStage(ctx, stage)
	require.NoError(t, err)
	assert.Equal(t, d.Revision(), result.ActiveRevision)
	changed := strings.Replace(string(d.Bytes()), "127.0.0.1:0]", "127.0.0.1:5354]", 1)
	require.NoError(t, os.WriteFile(p, []byte(changed), 0600))
	_, err = s.Reload(ctx)
	assert.Error(t, err)
	assert.True(t, s.Inspect().RestartRequired)
}
