package storage

import (
	"context"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/richkeenan/dimsum/internal/clients"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type clientNameStore interface {
	SaveClientNames(context.Context, string, []clients.Name) error
	LoadClientNames(context.Context) (string, []clients.Name, error)
}

func TestClientNamesReopenReplaceAndRollback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.sqlite")
	db, err := Open(path)
	require.NoError(t, err)
	s, ok := any(db).(clientNameStore)
	if !assert.True(t, ok, "storage must persist discovered client names") {
		require.NoError(t, db.Close())
		return
	}
	ctx := t.Context()
	scope := strings.Repeat("a", 64)
	n := clients.Name{Address: netip.MustParseAddr("192.0.2.20"), Name: "Work laptop", Source: "mdns", Updated: testStart, Expires: testStart.Add(time.Minute),
		Device: &clients.Enrichment{Category: "laptop", Hostname: "work-macbook.local", Evidence: []clients.Evidence{}}}
	require.NoError(t, s.SaveClientNames(ctx, scope, []clients.Name{n}))
	require.NoError(t, db.Close())
	db, err = Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, db.Close()) })
	s = any(db).(clientNameStore)
	gotScope, rows, err := s.LoadClientNames(ctx)
	require.NoError(t, err)
	assert.Equal(t, scope, gotScope)
	require.Len(t, rows, 1)
	assert.Equal(t, "Work laptop", rows[0].Name)
	assert.Equal(t, "laptop", rows[0].Device.Category)
	assert.True(t, testStart.Equal(rows[0].Updated))
	second := n
	second.Address = netip.MustParseAddr("192.0.2.21")
	require.NoError(t, s.SaveClientNames(ctx, scope, []clients.Name{n, second}))
	// No-op checkpoints should not rewrite every name to the WAL.
	_, err = db.write.Exec(`CREATE TRIGGER reject_name_update BEFORE UPDATE ON client_names BEGIN SELECT RAISE(ABORT,'fixture'); END`)
	require.NoError(t, err)
	require.NoError(t, s.SaveClientNames(ctx, scope, []clients.Name{n, second}))
	replacement := n
	replacement.Name = "Replacement"
	require.Error(t, s.SaveClientNames(ctx, scope, []clients.Name{replacement}))
	_, rows, err = s.LoadClientNames(ctx)
	require.NoError(t, err)
	require.Len(t, rows, 2, "failed update must also roll back removal of the second name")
	assert.Equal(t, "Work laptop", rows[0].Name)
	_, err = db.write.Exec("DROP TRIGGER reject_name_update")
	require.NoError(t, err)
	require.NoError(t, s.SaveClientNames(ctx, scope, []clients.Name{replacement}))
	_, rows, err = s.LoadClientNames(ctx)
	require.NoError(t, err)
	assert.Equal(t, "Replacement", rows[0].Name)
	require.Error(t, s.SaveClientNames(ctx, scope, make([]clients.Name, 8193)))
	require.NoError(t, s.SaveClientNames(ctx, scope, nil))
	_, rows, err = s.LoadClientNames(ctx)
	require.NoError(t, err)
	assert.Empty(t, rows)
}
