package control

import (
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/richkeenan/dimsum/internal/config"
	"github.com/richkeenan/dimsum/internal/dhcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func dhcpFixture(t *testing.T) (*Service, *config.Store) {
	t.Helper()
	b, e := os.ReadFile("../../testdata/config/dimsum.yaml")
	require.NoError(t, e)
	dir := t.TempDir()
	path := filepath.Join(dir, "dimsum.yaml")
	require.NoError(t, os.WriteFile(path, b, 0600))
	store, e := config.OpenStore(t.Context(), path, filepath.Join(dir, "state"), config.StoreOptions{Offline: true})
	require.NoError(t, e)
	return New(Options{Store: store, ConfigPath: path}), store
}

func TestDHCPReservationOptionalFieldAndScope(t *testing.T) {
	s, store := dhcpFixture(t)
	_, e := s.DHCPMutate(t.Context(), "PATCH", "", false, DHCPMutation{Revision: store.Inspect().SavedRevision, Edits: []config.Edit{{Path: []string{"subnet"}, Value: "192.0.2.0/24"}}})
	require.NoError(t, e)
	_, e = s.DHCPMutate(t.Context(), "POST", "", true, DHCPMutation{Revision: store.Inspect().SavedRevision, Item: &dhcp.Reservation{ID: "printer", MAC: "02:00:00:00:00:10", Address: "192.0.2.20"}})
	require.NoError(t, e)
	_, e = s.DHCPMutate(t.Context(), "PATCH", "printer", true, DHCPMutation{Revision: store.Inspect().SavedRevision, Edits: []config.Edit{{Path: []string{"hostname"}, Value: "lab-printer"}}})
	require.NoError(t, e)
	assert.Equal(t, "lab-printer", store.Snapshot().Config().DHCP.Reservations[0].Hostname)
	_, e = s.DHCPMutate(t.Context(), "PATCH", "", false, DHCPMutation{Revision: store.Inspect().SavedRevision, Edits: []config.Edit{{Path: []string{"dns", "listen"}, Value: "bad"}}})
	assert.ErrorIs(t, e, BadRequest)
}

func TestDHCPLeasePagesAndInvalidation(t *testing.T) {
	s, _ := dhcpFixture(t)
	snap := dhcp.LeaseSnapshot{Generation: 7, Revision: 9}
	for i := 300; i > 0; i-- {
		snap.Leases = append(snap.Leases, dhcp.Lease{Address: netip.AddrFrom4([4]byte{192, 0, byte(i / 256), byte(i % 256)}), Identity: "id:\x00\xff", State: dhcp.Bound})
	}
	s.options.DHCPInspect = func() dhcp.LeaseSnapshot { c := snap; c.Leases = append([]dhcp.Lease{}, snap.Leases...); return c }
	q := url.Values{"limit": {"256"}}
	defaultPage, e := s.DHCPLeases(nil)
	require.NoError(t, e)
	assert.Len(t, defaultPage.Items, 100)
	first, e := s.DHCPLeases(q)
	require.NoError(t, e)
	require.Len(t, first.Items, 256)
	assert.Equal(t, "00ff", first.Items[0].ClientID)
	q.Set("cursor", first.NextCursor)
	last, e := s.DHCPLeases(q)
	require.NoError(t, e)
	assert.Len(t, last.Items, 44)
	assert.Empty(t, last.NextCursor)
	assert.NotEqual(t, first.Items[255].Address, last.Items[0].Address)
	q.Set("state", "bound")
	_, e = s.DHCPLeases(q)
	assert.ErrorIs(t, e, ErrLeaseCursor)
	q.Del("state")
	snap.Revision++
	_, e = s.DHCPLeases(q)
	assert.ErrorIs(t, e, ErrLeaseCursor)
	snap.Revision--
	s.options.BootID = "new-boot"
	_, e = s.DHCPLeases(q)
	assert.ErrorIs(t, e, ErrLeaseCursor)
	for _, raw := range []string{"limit=257", "limit=0", "limit=1&limit=2", "cursor=broken", "state=free", "address=::1", "mac=bad", "client_id=gg", "unknown=x"} {
		q, _ := url.ParseQuery(raw)
		_, e = s.DHCPLeases(q)
		assert.ErrorIs(t, e, BadRequest, raw)
	}
	snap.Leases = make([]dhcp.Lease, 4097)
	_, e = s.DHCPLeases(nil)
	assert.ErrorIs(t, e, ErrBusy)
}
