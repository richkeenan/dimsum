package config

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultSecretPreservesExistingAndRestoredCredentials(t *testing.T) {
	s, d := secretStore(t, StoreOptions{Offline: true})
	require.NoError(t, s.EnsureAdminSecret(fixtureSecret(1)))
	require.NoError(t, s.EnsureAdminSecret(fixtureSecret(2)))
	got, err := s.ActiveSecret(AdminSecretName)
	require.NoError(t, err)
	assert.Equal(t, fixtureSecret(1), got)
	assert.Equal(t, d.Revision(), s.Snapshot().Revision())
	_, err = s.SetAdminSecret(t.Context(), d.Revision(), fixtureSecret(3))
	require.NoError(t, err)
	require.NoError(t, s.EnsureAdminSecret(fixtureSecret(4)))
	got, err = s.ActiveSecret(AdminSecretName)
	require.NoError(t, err)
	assert.Equal(t, fixtureSecret(3), got)
	c := s.Snapshot().Config()
	require.NoError(t, os.Remove(filepath.Join(s.ResolvePath(c.Paths.SecretsDir), "generations", c.Admin.SecretGeneration, AdminSecretName)))
	require.Error(t, s.EnsureAdminSecret(fixtureSecret(5)))
	_, err = s.ActiveSecret(AdminSecretName)
	assert.Error(t, err)
}

func TestPasswordConcurrentWritersAndExternalEdits(t *testing.T) {
	s, d := secretStore(t, StoreOptions{Offline: true})
	writeRootSecret(t, s, fixtureSecret(1))
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, seed := range []byte{2, 3} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := s.SetAdminSecret(t.Context(), d.Revision(), fixtureSecret(seed))
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else {
			assert.ErrorIs(t, err, ErrConflict)
			conflicts++
		}
	}
	assert.Equal(t, 1, successes)
	assert.Equal(t, 1, conflicts)
	before, err := s.ActiveSecret(AdminSecretName)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(s.path, []byte("invalid external edit"), 0600))
	_, err = s.SetAdminSecret(t.Context(), s.Snapshot().Revision(), fixtureSecret(4))
	assert.ErrorIs(t, err, ErrConflict)
	after, err := s.ActiveSecret(AdminSecretName)
	require.NoError(t, err)
	assert.Equal(t, before, after)
	saved, err := os.ReadFile(s.path)
	require.NoError(t, err)
	assert.Equal(t, "invalid external edit", string(saved))
}

func TestPasswordGenerationConflictRollbackAndRestart(t *testing.T) {
	s, d := secretStore(t, StoreOptions{Offline: true})
	old := fixtureSecret(1)
	writeRootSecret(t, s, old)
	archive, err := s.Backup()
	require.NoError(t, err)
	result, err := s.SetAdminSecret(t.Context(), d.Revision(), fixtureSecret(2))
	require.NoError(t, err)
	_, err = s.SetAdminSecret(t.Context(), d.Revision(), fixtureSecret(3))
	assert.ErrorIs(t, err, ErrConflict)
	restored, err := OpenStore(t.Context(), s.path, s.state, StoreOptions{Offline: true})
	require.NoError(t, err)
	got, err := restored.ActiveSecret(AdminSecretName)
	require.NoError(t, err)
	assert.Equal(t, fixtureSecret(2), got)
	_, err = s.Restore(t.Context(), result.SavedRevision, archive)
	require.NoError(t, err)
	got, err = s.ActiveSecret(AdminSecretName)
	require.NoError(t, err)
	assert.Equal(t, old, got)
	before, err := os.ReadFile(s.path)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = s.SetAdminSecret(ctx, s.Snapshot().Revision(), fixtureSecret(4))
	require.Error(t, err)
	after, err := os.ReadFile(s.path)
	require.NoError(t, err)
	assert.Equal(t, before, after)
	got, err = s.ActiveSecret(AdminSecretName)
	require.NoError(t, err)
	assert.Equal(t, old, got)
}
