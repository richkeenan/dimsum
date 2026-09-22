package dhcp

import (
	"context"
	"encoding/hex"
	"errors"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func migrationReservation(r Request) Reservation {
	return Reservation{ID: "phone", ClientID: hex.EncodeToString([]byte(r.ClientID)), Address: "192.0.2.150"}
}

func TestReservationMigrationRequestsNAKOldAddress(t *testing.T) {
	for _, phase := range []string{"renew", "reboot", "select"} {
		t.Run(phase, func(t *testing.T) {
			s := fixtureSettings()
			e, _ := engineFixture(t, s)
			r := client(1, Discover)
			r.ClientID = "synthetic-phone"
			old := bind(t, e, r)
			s.Reservations = []Reservation{migrationReservation(r)}
			require.NoError(t, e.Apply(s, 2))
			r.Type = RequestMessage
			if phase == "renew" {
				r.CIAddr = old.Address
			} else {
				r.RequestedIP = old.Address
				if phase == "select" {
					r.ServerID = netip.MustParseAddr(s.ServerIP)
				}
			}
			out := e.Handle(r)
			require.NotNil(t, out.Reply)
			assert.Equal(t, NAK, out.Reply.Type)
			assert.Nil(t, out.Mutation)
			assert.Equal(t, []Lease{old}, e.DurableLeases())
		})
	}
}

func TestReservationMigrationDurableBarrierAndRecovery(t *testing.T) {
	store, dir, now := storeFixture(t)
	s := fixtureSettings()
	e, err := NewEngine(s, 1, func() time.Time { return now })
	require.NoError(t, err)
	r := client(1, Discover)
	r.ClientID = "synthetic-phone"
	old := bind(t, e, r)
	storePut(t, store, 1, old)
	s.Reservations = []Reservation{migrationReservation(r)}
	require.NoError(t, e.Apply(s, 2))
	out := e.Handle(r)
	require.NotNil(t, out.Mutation)
	assert.Nil(t, out.Probe)
	assert.Nil(t, out.Reply)
	assert.Equal(t, Quarantined, out.Mutation.Lease.State)
	assert.Equal(t, old.HoldUntil, out.Mutation.Lease.HoldUntil)
	assert.Equal(t, old.Expiry, out.Mutation.Lease.Expiry)
	assert.Equal(t, Outcome{}, e.Handle(r))
	// Definite failure retains the old durable identity; retry must persist the
	// retirement again rather than creating a second bound row.
	e.CompleteCommit(CommitResult{Token: out.Mutation.Token, Err: errors.New("queue full")})
	assert.Equal(t, []Lease{old}, e.DurableLeases())
	out = e.Handle(r)
	require.NotNil(t, out.Mutation)
	require.NoError(t, store.Submit(context.Background(), *out.Mutation))
	result := storeResult(t, store)
	require.NoError(t, result.Err)
	out = e.CompleteCommit(result)
	require.NotNil(t, out.Probe)
	assert.Equal(t, netip.MustParseAddr("192.0.2.150"), out.Probe.Address)
	// Restart between the retirement commit and the new grant.
	require.NoError(t, store.Close(context.Background()))
	reopened, recovery, err := OpenLeaseStore(dir, 1024, func() time.Time { return now })
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopened.Close(context.Background())) })
	e, err = NewEngine(s, 3, func() time.Time { return now })
	require.NoError(t, err)
	require.NoError(t, e.Restore(recovery.Leases, recovery.LastKnown))
	o := offered(t, e, r)
	assert.Equal(t, "192.0.2.150", o.Address.String())
	r.Type, r.RequestedIP, r.ServerID = RequestMessage, o.Address, netip.MustParseAddr(s.ServerIP)
	out = e.Handle(r)
	require.NotNil(t, out.Mutation)
	require.NoError(t, reopened.Submit(context.Background(), *out.Mutation))
	result = storeResult(t, reopened)
	require.NoError(t, result.Err, "unique bound_identity index must accept migration")
	ack := e.CompleteCommit(result)
	require.NotNil(t, ack.Reply)
	assert.Equal(t, ACK, ack.Reply.Type)
	rows := e.DurableLeases()
	require.Len(t, rows, 2)
	assert.Equal(t, old.Address, rows[0].Address)
	assert.Equal(t, old.HoldUntil, rows[0].HoldUntil)
	require.NoError(t, reopened.Close(context.Background()))
	finalStore, finalRecovery, err := OpenLeaseStore(dir, 1024, func() time.Time { return now })
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, finalStore.Close(context.Background())) })
	assert.Equal(t, rows, finalRecovery.Leases)
	fresh, err := NewEngine(s, 4, func() time.Time { return now })
	require.NoError(t, err)
	require.NoError(t, fresh.Restore(finalRecovery.Leases, finalRecovery.LastKnown))
	other := client(2, Discover)
	other.RequestedIP = old.Address
	p := fresh.Handle(other)
	require.NotNil(t, p.Probe)
	assert.NotEqual(t, old.Address, p.Probe.Address, "old promise must remain unavailable")
	r.RequestedIP, r.ServerID, r.CIAddr = netip.Addr{}, netip.Addr{}, old.Address
	nak := fresh.Handle(r)
	require.NotNil(t, nak.Reply)
	assert.Equal(t, NAK, nak.Reply.Type)
	// Expiry alone does not release the retired address: its delete must commit.
	now = old.HoldUntil.Add(time.Second)
	mutations := fresh.Tick()
	var retirement *Mutation
	for i := range mutations {
		if mutations[i].Lease.Address == old.Address {
			retirement = &mutations[i]
		}
	}
	require.NotNil(t, retirement)
	fresh.CompleteCommit(CommitResult{Token: retirement.Token, Err: errors.New("delete rollback")})
	other = client(3, Discover)
	other.RequestedIP = old.Address
	p = fresh.Handle(other)
	require.NotNil(t, p.Probe)
	assert.NotEqual(t, old.Address, p.Probe.Address)
	for _, m := range fresh.Tick() {
		if m.Lease.Address == old.Address {
			fresh.CompleteCommit(CommitResult{Token: m.Token})
		}
	}
	other = client(4, Discover)
	other.RequestedIP = old.Address
	p = fresh.Handle(other)
	require.NotNil(t, p.Probe)
	assert.Equal(t, old.Address, p.Probe.Address)
}

func TestReservationMigrationStoreFailure(t *testing.T) {
	store, dir, now := storeFixture(t)
	s := fixtureSettings()
	e, err := NewEngine(s, 1, func() time.Time { return now })
	require.NoError(t, err)
	r := client(1, Discover)
	r.ClientID = "synthetic-phone"
	old := bind(t, e, r)
	storePut(t, store, 1, old)
	s.Reservations = []Reservation{migrationReservation(r)}
	require.NoError(t, e.Apply(s, 2))
	out := e.Handle(r)
	require.NotNil(t, out.Mutation)
	store.begin = func() (leaseTransaction, error) { return nil, errors.New("disk failure") }
	require.NoError(t, store.Submit(context.Background(), *out.Mutation))
	result := storeResult(t, store)
	require.Error(t, result.Err)
	assert.Equal(t, Outcome{}, e.CompleteCommit(result))
	assert.Equal(t, []Lease{old}, e.DurableLeases())
	out = e.Handle(r)
	require.NotNil(t, out.Mutation)
	assert.Nil(t, out.Probe)
	assert.Error(t, store.Submit(context.Background(), *out.Mutation))
	require.NoError(t, store.Close(context.Background()))
	reopened, recovery, err := OpenLeaseStore(dir, 1024, func() time.Time { return now })
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopened.Close(context.Background())) })
	assert.Equal(t, []Lease{old}, recovery.Leases)
	fresh, err := NewEngine(s, 3, func() time.Time { return now })
	require.NoError(t, err)
	require.NoError(t, fresh.Restore(recovery.Leases, recovery.LastKnown))
	out = fresh.Handle(r)
	require.NotNil(t, out.Mutation)
	assert.Nil(t, out.Probe)
}

func TestReservationMigrationProcessCrash(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	r := client(1, Discover)
	r.ClientID = "synthetic-phone"
	s := fixtureSettings()
	s.Reservations = []Reservation{migrationReservation(r)}
	if point := os.Getenv("DIMSUM_MIGRATION_CRASH"); point != "" {
		store, recovery, err := OpenLeaseStore(os.Getenv("DIMSUM_STORE_DIR"), 1024, func() time.Time { return now })
		require.NoError(t, err)
		e, err := NewEngine(s, 2, func() time.Time { return now })
		require.NoError(t, err)
		require.NoError(t, e.Restore(recovery.Leases, recovery.LastKnown))
		out := e.Handle(r)
		require.NotNil(t, out.Mutation)
		begin := store.begin
		store.begin = func() (leaseTransaction, error) { tx, err := begin(); return crashTransaction{tx, point}, err }
		require.NoError(t, store.Submit(context.Background(), *out.Mutation))
		storeResult(t, store)
		t.Fatal("expected process exit during retirement commit")
	}
	for _, point := range []string{"before", "after"} {
		t.Run(point, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "dhcp")
			store, _, err := OpenLeaseStore(dir, 1024, func() time.Time { return now })
			require.NoError(t, err)
			e, err := NewEngine(fixtureSettings(), 1, func() time.Time { return now })
			require.NoError(t, err)
			old := bind(t, e, r)
			storePut(t, store, 1, old)
			require.NoError(t, store.Close(context.Background()))
			cmd := exec.Command(os.Args[0], "-test.run=^TestReservationMigrationProcessCrash$")
			cmd.Env = append(os.Environ(), "DIMSUM_MIGRATION_CRASH="+point, "DIMSUM_STORE_DIR="+dir)
			output, err := cmd.CombinedOutput()
			var exit *exec.ExitError
			require.ErrorAs(t, err, &exit, string(output))
			require.Equal(t, 23, exit.ExitCode(), string(output))
			reopened, recovery, err := OpenLeaseStore(dir, 1024, func() time.Time { return now })
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, reopened.Close(context.Background())) })
			require.Len(t, recovery.Leases, 1)
			assert.Equal(t, old.HoldUntil, recovery.Leases[0].HoldUntil)
			e, err = NewEngine(s, 3, func() time.Time { return now })
			require.NoError(t, err)
			require.NoError(t, e.Restore(recovery.Leases, recovery.LastKnown))
			out := e.Handle(r)
			if point == "before" {
				assert.Equal(t, Bound, recovery.Leases[0].State)
				require.NotNil(t, out.Mutation)
				assert.Nil(t, out.Probe)
			} else {
				assert.Equal(t, Quarantined, recovery.Leases[0].State)
				require.NotNil(t, out.Probe)
				assert.Equal(t, netip.MustParseAddr("192.0.2.150"), out.Probe.Address)
			}
		})
	}
}
