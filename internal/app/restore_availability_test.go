package app

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/richkeenan/dimsum/internal/config"
	"github.com/richkeenan/dimsum/internal/dhcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManagedRestoreRejectsUnavailableDHCPBeforePublication(t *testing.T) {
	t.Setenv("DIMSUM_DEPLOYMENT", "docker-desktop")
	dir := t.TempDir()
	path := filepath.Join(dir, "dimsum.yaml")
	source := []byte("version: 1\ndns:\n  listen: [0.0.0.0:53]\n  upstreams: [127.0.0.1:9]\nadmin:\n  listen: 127.0.0.1:0\npaths:\n  data_dir: data\n  secrets_dir: secrets\n")
	require.NoError(t, os.WriteFile(path, source, 0600))
	store, err := config.OpenStore(t.Context(), path, path+".state", config.StoreOptions{Offline: true})
	require.NoError(t, err)
	observations, err := newObservability(store)
	require.NoError(t, err)
	t.Cleanup(observations.close)
	m, err := newManagedRuntime(new(Service), store, observations, "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { m.control.Close(); assert.NoError(t, m.local.Close()) })
	before := store.Inspect()
	oldSecret, err := store.ActiveSecret(config.AdminSecretName)
	require.NoError(t, err)
	secretRoot := store.ResolvePath(store.Snapshot().Config().Paths.SecretsDir)
	entriesBefore, err := os.ReadDir(secretRoot)
	require.NoError(t, err)
	candidate, err := config.Parse(append(bytes.Clone(source), []byte("dhcp:\n  enabled: true\n  interface: eth0\n  server_ip: 192.0.2.2\n  subnet: 192.0.2.0/24\n  gateway: 192.0.2.1\n  range_start: 192.0.2.100\n  range_end: 192.0.2.199\n  lease_seconds: 86400\n  local_domain: home.arpa\n")...))
	require.NoError(t, err, "offline parsing remains portable")
	replacement := []byte("pbkdf2-sha256$600000$" + base64.RawStdEncoding.EncodeToString(bytes.Repeat([]byte{2}, 16)) + "$" + base64.RawStdEncoding.EncodeToString(bytes.Repeat([]byte{2}, 32)) + "\n")
	archive, err := config.BackupWithSecrets(candidate, map[string][]byte{config.AdminSecretName: replacement})
	require.NoError(t, err)
	input, err := json.Marshal(map[string]string{"revision": before.SavedRevision, "archive": base64.StdEncoding.EncodeToString(archive)})
	require.NoError(t, err)
	_, err = m.control.StartJob(t.Context(), "restore", input)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return m.control.Jobs()[0].State != "running"
	}, 5*time.Second, 10*time.Millisecond)
	jobs := m.control.Jobs()
	assert.Equal(t, "failed", jobs[0].State)
	assert.Equal(t, dhcp.CurrentAvailability().Reason, jobs[0].Error)
	after := store.Inspect()
	assert.Equal(t, before.SavedRevision, after.SavedRevision)
	assert.Equal(t, before.ActiveRevision, after.ActiveRevision)
	assert.Equal(t, before.ActiveGeneration, after.ActiveGeneration)
	saved, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, source, saved)
	secret, err := store.ActiveSecret(config.AdminSecretName)
	require.NoError(t, err)
	assert.Equal(t, oldSecret, secret)
	entriesAfter, err := os.ReadDir(secretRoot)
	require.NoError(t, err)
	assert.Equal(t, entriesBefore, entriesAfter, "rejection must not even create credential staging directories")

	// The same deployed hook still accepts a backup with DHCP disabled.
	candidate, err = candidate.Upsert([]config.Edit{{Path: []string{"dhcp", "enabled"}, Value: false}})
	require.NoError(t, err)
	archive, err = config.BackupWithSecrets(candidate, map[string][]byte{config.AdminSecretName: replacement})
	require.NoError(t, err)
	input, err = json.Marshal(map[string]string{"revision": before.SavedRevision, "archive": base64.StdEncoding.EncodeToString(archive)})
	require.NoError(t, err)
	_, err = m.control.StartJob(t.Context(), "restore", input)
	require.NoError(t, err)
	require.Eventually(t, func() bool { return m.control.Jobs()[1].State != "running" }, 5*time.Second, 10*time.Millisecond)
	assert.Equal(t, "succeeded", m.control.Jobs()[1].State)
	secret, err = store.ActiveSecret(config.AdminSecretName)
	require.NoError(t, err)
	assert.Equal(t, replacement, secret)
}
