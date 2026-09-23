package app

import (
	"database/sql"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/richkeenan/dimsum/internal/clients"
	"github.com/richkeenan/dimsum/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNameCheckpointFailureIsVisibleAndRetryKeepsOriginalDeadline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.sqlite")
	db, err := storage.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, db.Close()) })
	view, err := clients.NewView(clients.Settings{MDNS: clients.MDNSSettings{Enabled: true}}, nil, nil)
	require.NoError(t, err)
	m := clients.New(func() *clients.View { return view })
	now := time.Now().UTC()
	scope, _ := m.NameSnapshot(now)
	n := clients.Name{Address: netip.MustParseAddr("192.0.2.20"), Name: "Example laptop", Source: "mdns", Updated: now.Add(-time.Hour), Expires: now.Add(-time.Minute)}
	m.RestoreNames(scope, []clients.Name{n}, now)
	persistence := &clientNamePersistence{db: db, names: m}
	raw, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, raw.Close()) })
	_, err = raw.Exec(`CREATE TRIGGER fail_name_save BEFORE INSERT ON client_names BEGIN SELECT RAISE(ABORT,'fixture'); END`)
	require.NoError(t, err)
	persistence.save(t.Context())
	assert.Contains(t, strings.Join(m.Diagnostics().Errors, "\n"), "Client name persistence:")
	assert.Equal(t, "Example laptop", m.Get(n.Address).Name)
	_, err = raw.Exec("DROP TRIGGER fail_name_save")
	require.NoError(t, err)
	persistence.save(t.Context())
	assert.Empty(t, m.Diagnostics().Errors)
	after := clients.New(func() *clients.View { return view })
	(&clientNamePersistence{db: db, names: after}).restore(t.Context())
	assert.Equal(t, "Example laptop", after.Get(n.Address).Name)
	assert.True(t, n.Updated.Equal(after.Get(n.Address).Updated))
	_, err = raw.Exec(`UPDATE client_names SET payload=x'00'`)
	require.NoError(t, err)
	broken := clients.New(func() *clients.View { return view })
	(&clientNamePersistence{db: db, names: broken}).restore(t.Context())
	assert.Contains(t, strings.Join(broken.Diagnostics().Errors, "\n"), "invalid persisted client name")
	assert.Empty(t, broken.Get(n.Address).Name)
}

func TestFailedNameRestoreCannotEraseUnreadIdentities(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.sqlite")
	db, err := storage.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, db.Close()) })
	view, err := clients.NewView(clients.Settings{MDNS: clients.MDNSSettings{Enabled: true}}, nil, nil)
	require.NoError(t, err)
	m := clients.New(func() *clients.View { return view })
	now := time.Now().UTC()
	scope, _ := m.NameSnapshot(now)
	sleeping := clients.Name{Address: netip.MustParseAddr("192.0.2.20"), Name: "Sleeping laptop", Source: "mdns", Updated: now.Add(-47 * time.Hour), Expires: now.Add(-46 * time.Hour)}
	old := sleeping
	old.Address, old.Name = netip.MustParseAddr("192.0.2.21"), "Old display"
	require.NoError(t, db.SaveClientNames(t.Context(), scope, []clients.Name{sleeping, old}))
	raw, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, raw.Close()) })
	// Temporarily hide the table to fail restoration without losing its rows.
	_, err = raw.Exec("ALTER TABLE client_names RENAME TO unavailable_names")
	require.NoError(t, err)
	persistence := &clientNamePersistence{db: db, names: m}
	persistence.restore(t.Context())
	require.NotEmpty(t, m.Diagnostics().Errors)
	// Discovery confirms a new occupant before storage recovers.
	live := old
	live.Name, live.Updated = "New display", now
	m.RestoreNames(scope, []clients.Name{live}, now)
	persistence.save(t.Context())
	_, err = raw.Exec("ALTER TABLE unavailable_names RENAME TO client_names")
	require.NoError(t, err)
	persistence.save(t.Context())
	assert.Equal(t, "Sleeping laptop", m.Get(sleeping.Address).Name)
	assert.Equal(t, "New display", m.Get(old.Address).Name)
	_, saved, err := db.LoadClientNames(t.Context())
	require.NoError(t, err)
	require.Len(t, saved, 2, "retry must recover unread names before replacing the cache")
	assert.True(t, sleeping.Updated.Equal(saved[0].Updated))
	assert.Equal(t, "New display", saved[1].Name)
	assert.Empty(t, m.Diagnostics().Errors)
}
