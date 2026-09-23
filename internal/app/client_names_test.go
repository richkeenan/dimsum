package app

import (
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/richkeenan/dimsum/internal/clients"
	"github.com/richkeenan/dimsum/internal/config"
	"github.com/richkeenan/dimsum/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManagedClientNamesAvailableOnFirstReadAndFlushedOnShutdown(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	text := "version: 1\ndns:\n  listen: [127.0.0.1:0]\n  upstreams: [127.0.0.1:9]\nadmin:\n  listen: 127.0.0.1:0\npaths:\n  data_dir: data\n  secrets_dir: secrets\nnaming:\n  mdns:\n    enabled: true\n    interfaces: [dimsum-fixture0]\n"
	require.NoError(t, os.WriteFile(path, []byte(text), 0600))
	store, err := config.OpenStore(t.Context(), path, path+".state", config.StoreOptions{Offline: true})
	require.NoError(t, err)
	view := store.Snapshot().Names()
	fixture := clients.New(func() *clients.View { return view })
	now := time.Now().UTC()
	scope, _ := fixture.NameSnapshot(now)
	address := netip.MustParseAddr("192.0.2.20")
	n := clients.Name{Address: address, Name: "Work laptop", Source: "mdns", Updated: now.Add(-47 * time.Hour), Expires: now.Add(-46 * time.Hour),
		Device: &clients.Enrichment{Hostname: "work-macbook.local", Category: "laptop", Evidence: []clients.Evidence{}}}
	dbPath := filepath.Join(dir, "data", "history.sqlite")
	require.NoError(t, os.MkdirAll(filepath.Dir(dbPath), 0700))
	db, err := storage.Open(dbPath)
	require.NoError(t, err)
	require.NoError(t, db.SaveClientNames(t.Context(), scope, []clients.Name{n}))
	require.NoError(t, db.Close())
	s := new(Service)
	require.NoError(t, s.StartManaged(t.Context(), store))
	t.Cleanup(func() { assert.NoError(t, s.Close()) })
	first := s.ClientName(address)
	assert.Equal(t, "Work laptop", first.Name, "the first read must not wait for rediscovery")
	assert.Equal(t, "laptop", first.Device.Category)
	assert.True(t, n.Updated.Equal(first.Updated))
	// Change in memory, then close immediately, before the periodic checkpoint.
	n.Name, n.Updated = "Renamed laptop", now
	s.names.RestoreNames(scope, []clients.Name{n}, now)
	require.NoError(t, s.Close())
	db, err = storage.Open(dbPath)
	require.NoError(t, err)
	_, rows, err := db.LoadClientNames(t.Context())
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "Renamed laptop", rows[0].Name)
	assert.True(t, now.Equal(rows[0].Updated))
	require.NoError(t, db.Close())
	restarted := new(Service)
	require.NoError(t, restarted.StartManaged(t.Context(), store))
	t.Cleanup(func() { assert.NoError(t, restarted.Close()) })
	assert.Equal(t, "Renamed laptop", restarted.ClientName(address).Name)
	// A new discovery also reaches disk while the service is still running.
	n.Name, n.Updated = "Live laptop", time.Now().UTC()
	restarted.names.RestoreNames(scope, []clients.Name{n}, n.Updated)
	require.Eventually(t, func() bool {
		_, rows, err := restarted.observability.db.LoadClientNames(t.Context())
		return err == nil && len(rows) == 1 && rows[0].Name == "Live laptop"
	}, 8*time.Second, 20*time.Millisecond)
	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, text, string(contents), "discovery must never alter authoritative config")
}
