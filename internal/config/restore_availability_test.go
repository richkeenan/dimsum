package config

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRestoreDeploymentChecksPrecedeCredentialStaging(t *testing.T) {
	d, err := Parse(bytes.Replace([]byte(backupSample), []byte(`listen: ["127.0.0.1:0"]`), []byte(`listen: ["0.0.0.0:53"]`), 1))
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, d.Bytes(), 0600))
	s, err := OpenStore(t.Context(), path, path+".state", StoreOptions{Offline: true})
	require.NoError(t, err)
	root := writeRootSecret(t, s, fixtureSecret(1))
	candidate, err := Parse(append(d.Bytes(), []byte("dhcp:\n  enabled: true\n  interface: eth0\n  server_ip: 192.0.2.2\n  subnet: 192.0.2.0/24\n  gateway: 192.0.2.1\n  range_start: 192.0.2.100\n  range_end: 192.0.2.199\n  lease_seconds: 86400\n  local_domain: home.arpa\n")...))
	require.NoError(t, err)
	archive, err := BackupWithSecrets(candidate, map[string][]byte{AdminSecretName: fixtureSecret(2)})
	require.NoError(t, err)
	before := s.Inspect()
	unavailable := errors.New("deployment does not support DHCP")
	checked := false
	result, err := s.Restore(t.Context(), before.SavedRevision, archive, func(decoded *Document) error {
		checked = true
		assert.Equal(t, candidate.Bytes(), decoded.Bytes())
		if decoded.Config().DHCP.Enabled {
			return unavailable
		}
		return nil
	})
	require.ErrorIs(t, err, unavailable)
	assert.True(t, checked)
	assert.Equal(t, before, result)
	assert.Equal(t, before, s.Inspect())
	saved, err := os.ReadFile(s.ConfigPath())
	require.NoError(t, err)
	assert.Equal(t, d.Bytes(), saved)
	secret, err := s.ActiveSecret(AdminSecretName)
	require.NoError(t, err)
	assert.Equal(t, fixtureSecret(1), secret)
	_, err = os.Stat(filepath.Join(filepath.Dir(root), "generations"))
	assert.True(t, os.IsNotExist(err), "deployment rejection must happen before staging")

	// Generic offline restore remains portable when no deployment check is supplied.
	result, err = s.Restore(t.Context(), before.SavedRevision, archive)
	require.NoError(t, err)
	assert.True(t, s.Snapshot().Config().DHCP.Enabled)
	assert.Greater(t, result.ActiveGeneration, before.ActiveGeneration)
	secret, err = s.ActiveSecret(AdminSecretName)
	require.NoError(t, err)
	assert.Equal(t, fixtureSecret(2), secret)
}
