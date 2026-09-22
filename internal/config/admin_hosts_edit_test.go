package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdminAllowedHostsSettingsEdits(t *testing.T) {
	d, err := Parse([]byte("# preserved\nversion: 1\ndns:\n  listen: [127.0.0.1:5353]\nadmin:\n  listen: 127.0.0.1:8080\npaths:\n  data_dir: ./data\n  secrets_dir: ./secrets\n"))
	require.NoError(t, err)
	d, err = d.Upsert([]Edit{{Path: []string{"admin", "allowed_hosts"}, Value: []any{"localhost:18080", "127.0.0.1:18080"}}})
	require.NoError(t, err)
	assert.Equal(t, []string{"localhost:18080", "127.0.0.1:18080"}, d.Config().Admin.AllowedHosts)
	assert.Contains(t, string(d.Bytes()), "# preserved\n")
	d, err = d.Upsert([]Edit{{Path: []string{"admin", "allowed_hosts"}, Value: []any{"localhost:18081"}}})
	require.NoError(t, err)
	assert.Equal(t, []string{"localhost:18081"}, d.Config().Admin.AllowedHosts)
	_, err = d.Upsert([]Edit{{Path: []string{"admin", "allowed_hosts"}, Value: []any{"https://example.test"}}})
	require.Error(t, err, "settings edits must retain host validation")
}
