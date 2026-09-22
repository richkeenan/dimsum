package control

import (
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/richkeenan/dimsum/internal/config"
	"github.com/richkeenan/dimsum/internal/dhcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func dhcpFixture(t *testing.T, network ...bool) (*Service, *config.Store) {
	t.Helper()
	b, e := os.ReadFile("../../testdata/config/dimsum.yaml")
	require.NoError(t, e)
	if len(network) > 0 {
		b = []byte(strings.Replace(string(b), "127.0.0.1:0", "0.0.0.0:53", 1))
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "dimsum.yaml")
	require.NoError(t, os.WriteFile(path, b, 0600))
	store, e := config.OpenStore(t.Context(), path, filepath.Join(dir, "state"), config.StoreOptions{Offline: true})
	require.NoError(t, e)
	return New(Options{Store: store, ConfigPath: path}), store
}

func TestUnsupportedDHCPRejectsEnablementWithoutSaving(t *testing.T) {
	t.Setenv("DIMSUM_DEPLOYMENT", "docker-desktop")
	s, store := dhcpFixture(t, true)
	// A valid, disabled configuration isolates capability rejection from validation.
	edits := []config.Edit{
		{Path: []string{"interface"}, Value: "eth0"},
		{Path: []string{"server_ip"}, Value: "192.0.2.2"},
		{Path: []string{"subnet"}, Value: "192.0.2.0/24"},
		{Path: []string{"gateway"}, Value: "192.0.2.1"},
		{Path: []string{"range_start"}, Value: "192.0.2.100"},
		{Path: []string{"range_end"}, Value: "192.0.2.150"},
		{Path: []string{"lease_seconds"}, Value: 86400},
		{Path: []string{"local_domain"}, Value: "home.arpa"},
	}
	_, err := s.DHCPMutate(t.Context(), "PATCH", "", false, DHCPMutation{Revision: store.Inspect().SavedRevision, Edits: edits})
	require.NoError(t, err)
	revision := store.Inspect().SavedRevision
	_, err = s.DHCPMutate(t.Context(), "PATCH", "", false, DHCPMutation{Revision: revision, Edits: []config.Edit{{Path: []string{"enabled"}, Value: true}}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Docker Desktop")
	assert.Equal(t, revision, store.Inspect().SavedRevision)
	mutation := func() Mutation {
		return Mutation{Revision: revision, Edits: []config.Edit{{Path: []string{"dhcp", "enabled"}, Value: true}}}
	}
	_, err = s.Mutate(t.Context(), "settings", "PATCH", mutation())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Docker Desktop")
	_, err = s.Stage(mutation())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Docker Desktop")
	assert.Equal(t, revision, store.Inspect().SavedRevision)
	value, err := s.DHCPStatus()
	require.NoError(t, err)
	availability := value.(map[string]any)["availability"].(dhcp.Availability)
	assert.False(t, availability.Supported)
	assert.Equal(t, "docker_desktop", availability.Code)
	_, err = s.StartJob(t.Context(), "dhcp-check", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Docker Desktop")
	// Stages saved by another deployment cannot bypass the current capability.
	d, err := s.candidate("settings", "PATCH", mutation())
	require.NoError(t, err)
	id, err := store.Stage(revision, d)
	require.NoError(t, err)
	_, err = s.Commit(t.Context(), id)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Docker Desktop")
	assert.Equal(t, revision, store.Inspect().SavedRevision)
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

func TestDHCPLeaseSmallPageAllocationDoesNotScaleWithTable(t *testing.T) {
	s, _ := dhcpFixture(t)
	rows := make([]dhcp.Lease, 256)
	for i := range rows {
		rows[i] = dhcp.Lease{Address: netip.AddrFrom4([4]byte{192, 0, 2, byte(i)}), MAC: [6]byte{2, 0, 0, 0, 0, byte(i)}, Identity: "id:\x00\xff", State: dhcp.Bound}
	}
	count := 16
	s.options.DHCPInspect = func() dhcp.LeaseSnapshot {
		return dhcp.LeaseSnapshot{Generation: 7, Revision: 1, Leases: append([]dhcp.Lease(nil), rows[:count]...)}
	}
	var page DHCPLeasePage
	var err error
	q := url.Values{"limit": {"1"}}
	small := testing.AllocsPerRun(100, func() { page, err = s.DHCPLeases(q) })
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	count = 256
	large := testing.AllocsPerRun(100, func() { page, err = s.DHCPLeases(q) })
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	assert.LessOrEqual(t, large-small, float64(8), "small pages must not format every discarded row")
	t.Logf("one-item page allocations with 16/256 rows: %g/%g", small, large)
}

func TestDHCPFilteredLeasePageBoundaries(t *testing.T) {
	s, _ := dhcpFixture(t)
	var rows []dhcp.Lease
	for i := 1; i <= 10; i++ {
		state := dhcp.Bound
		if i%2 == 0 {
			state = dhcp.Offered
		}
		rows = append(rows, dhcp.Lease{Address: netip.AddrFrom4([4]byte{192, 0, 2, byte(i)}), State: state})
	}
	s.options.DHCPInspect = func() dhcp.LeaseSnapshot {
		return dhcp.LeaseSnapshot{Generation: 7, Revision: 1, Leases: append([]dhcp.Lease(nil), rows...)}
	}
	q := url.Values{"limit": {"2"}, "state": {"bound"}}
	var got []string
	for {
		page, err := s.DHCPLeases(q)
		require.NoError(t, err)
		for _, l := range page.Items {
			got = append(got, l.Address)
		}
		if page.NextCursor == "" {
			break
		}
		q.Set("cursor", page.NextCursor)
	}
	assert.Equal(t, []string{"192.0.2.1", "192.0.2.3", "192.0.2.5", "192.0.2.7", "192.0.2.9"}, got)
}
