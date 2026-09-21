package config_test

import (
	"github.com/richkeenan/dimsum/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestDiscoverySettingsUpsertPreservesOwnerText(t *testing.T) {
	d, err := config.Parse([]byte(sample + "\nclients:\n  - address: 192.0.2.20\n    name: Owner name # keep\n"))
	require.NoError(t, err)
	edits := []config.Edit{{Path: []string{"naming", "mdns", "enabled"}, Value: true}, {Path: []string{"naming", "mdns", "interfaces"}, Value: []any{"eth0", "eth1"}}}
	next, err := d.Upsert(edits)
	require.NoError(t, err)
	assert.True(t, next.Config().Naming.MDNS.Enabled)
	assert.Equal(t, []string{"eth0", "eth1"}, next.Config().Naming.MDNS.Interfaces)
	assert.Contains(t, string(next.Bytes()), "name: Owner name # keep")
	next, err = next.Upsert([]config.Edit{{Path: edits[1].Path, Value: []any{}}})
	require.NoError(t, err)
	assert.Empty(t, next.Config().Naming.MDNS.Interfaces)
	next, err = next.Upsert([]config.Edit{{Path: edits[1].Path, Value: []any{"eth2"}}})
	require.NoError(t, err)
	assert.Equal(t, []string{"eth2"}, next.Config().Naming.MDNS.Interfaces)
	_, err = next.Upsert([]config.Edit{{Path: edits[1].Path, Value: []any{"eth0", "eth0"}}})
	assert.Error(t, err)
	_, err = next.Upsert([]config.Edit{{Path: []string{"dns", "listen"}, Value: []any{"example"}}})
	assert.Error(t, err, "do not loosen arbitrary collection editing")
}
