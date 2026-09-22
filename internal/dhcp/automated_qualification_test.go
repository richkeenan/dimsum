package dhcp

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"net/netip"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type qualificationRollbackTransaction struct {
	leaseTransaction
	entered chan struct{}
	release <-chan struct{}
}

func (tx qualificationRollbackTransaction) Exec(query string, args ...any) (sql.Result, error) {
	result, err := tx.leaseTransaction.Exec(query, args...)
	if err != nil {
		return result, err
	}
	close(tx.entered)
	<-tx.release
	return result, errors.New("qualification delete rollback")
}

func qualificationDiscovery(t *testing.T, n byte, id []byte) *dhcpv4.DHCPv4 {
	t.Helper()
	p, err := dhcpv4.NewDiscovery(net.HardwareAddr{2, 0, 0, 0, 0, n})
	require.NoError(t, err)
	if id != nil {
		p.UpdateOption(dhcpv4.OptClientIdentifier(id))
	}
	return p
}

func qualificationACK(t *testing.T, link *runtimeLink, offer *dhcpv4.DHCPv4, id []byte) *dhcpv4.DHCPv4 {
	t.Helper()
	require.Equal(t, dhcpv4.MessageTypeOffer, offer.MessageType())
	r, err := dhcpv4.NewRequestFromOffer(offer)
	require.NoError(t, err)
	if id != nil {
		r.UpdateOption(dhcpv4.OptClientIdentifier(id))
	}
	link.input <- r.ToBytes()
	ack := wireReceive(t, link)
	assert.Equal(t, dhcpv4.MessageTypeAck, ack.MessageType())
	assert.Equal(t, r.TransactionID, ack.TransactionID)
	assert.Equal(t, offer.YourIPAddr, ack.YourIPAddr)
	return ack
}

// Two independent wire clients overlap during probing, fill a two-address pool,
// then go silent across a real SQLite close/reopen. An ARP-silent owner must not
// lose its unexpired address to a third client; only RELEASE makes room.
func TestAutomatedQualificationSimultaneousExhaustionRecovery(t *testing.T) {
	s := fixtureSettings()
	s.RangeEnd, s.MaxLeases = "192.0.2.101", 2
	dir := filepath.Join(t.TempDir(), "dhcp")
	store, rows, err := OpenLeaseStore(dir, s.MaxLeases, nil)
	require.NoError(t, err)
	entered, release := make(chan netip.Addr, 2), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	probe := func(ctx context.Context, ip netip.Addr) (bool, error) {
		entered <- ip
		select {
		case <-release:
			return false, nil
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
	link := newRuntimeLink()
	rt, err := StartRuntime(context.Background(), s, 1, link, probe, store, rows)
	require.NoError(t, err)
	t.Cleanup(func() { unblock(); assert.NoError(t, rt.Close(context.Background())) })
	clients := []*dhcpv4.DHCPv4{qualificationDiscovery(t, 1, nil), qualificationDiscovery(t, 2, nil)}
	for _, p := range clients {
		link.input <- p.ToBytes()
	}
	probed := make(map[netip.Addr]bool)
	for range clients {
		select {
		case ip := <-entered:
			assert.False(t, probed[ip], "simultaneous clients must not probe the same candidate")
			probed[ip] = true
		case <-time.After(3 * time.Second):
			t.Fatal("both clients did not reach overlapping probes")
		}
	}
	unblock()
	offers := []*dhcpv4.DHCPv4{wireReceive(t, link), wireReceive(t, link)}
	assert.NotEqual(t, offers[0].YourIPAddr, offers[1].YourIPAddr)
	for _, offer := range offers {
		qualificationACK(t, link, offer, nil)
	}
	require.NoError(t, rt.Close(context.Background()))
	store, rows, err = OpenLeaseStore(dir, s.MaxLeases, nil)
	require.NoError(t, err)
	require.Len(t, rows.Leases, 2)
	var probes atomic.Int32
	link = newRuntimeLink()
	rt, err = StartRuntime(context.Background(), s, 2, link, func(context.Context, netip.Addr) (bool, error) {
		probes.Add(1)
		return false, nil
	}, store, rows)
	require.NoError(t, err)
	third := qualificationDiscovery(t, 3, nil)
	link.input <- third.ToBytes()
	require.Eventually(t, func() bool { return rt.Status().Transport.Received >= 1 }, time.Second, time.Millisecond)
	select {
	case w := <-link.output:
		t.Errorf("exhausted pool replied to third client while owners slept: %x", w.Payload)
	case <-time.After(100 * time.Millisecond):
	}
	assert.Zero(t, probes.Load(), "silent unexpired owners must not even be ARP-probed for reuse")
	assert.Len(t, rt.Projection().Leases, 2)
	// Build RELEASE using the same independent codec as the acquiring client.
	rel, err := dhcpv4.NewRequestFromOffer(offers[0])
	require.NoError(t, err)
	rel.UpdateOption(dhcpv4.OptMessageType(dhcpv4.MessageTypeRelease))
	rel.ClientIPAddr = offers[0].YourIPAddr
	rel.DeleteOption(dhcpv4.OptionRequestedIPAddress)
	link.input <- rel.ToBytes()
	require.Eventually(t, func() bool { return len(rt.Projection().Leases) == 1 }, 3*time.Second, time.Millisecond)
	link.input <- third.ToBytes()
	offer := wireReceive(t, link)
	assert.Equal(t, offers[0].YourIPAddr, offer.YourIPAddr)
	qualificationACK(t, link, offer, nil)
	require.NoError(t, rt.Close(context.Background()))
	final, recovered, err := OpenLeaseStore(dir, s.MaxLeases, nil)
	require.NoError(t, err)
	defer final.Close(context.Background())
	require.Len(t, recovered.Leases, 2)
	owners := make(map[string]byte)
	for _, l := range recovered.Leases {
		assert.Equal(t, Bound, l.State)
		owners[l.Address.String()] = l.MAC[5]
	}
	assert.Equal(t, byte(3), owners[offers[0].YourIPAddr.String()])
	assert.Equal(t, offers[1].ClientHWAddr[5], owners[offers[1].YourIPAddr.String()])
}

func TestAutomatedQualificationReservations(t *testing.T) {
	for _, tc := range []struct {
		name         string
		reservations []Reservation
		id           []byte
		want         string
	}{
		{"in-pool-mac", []Reservation{{ID: "fixed", MAC: "02:00:00:00:00:01", Address: "192.0.2.100"}}, nil, "192.0.2.100"},
		{"outside-pool-mac", []Reservation{{ID: "fixed", MAC: "02:00:00:00:00:01", Address: "192.0.2.20"}}, nil, "192.0.2.20"},
		{"client-id-precedes-mac", []Reservation{{ID: "mac", MAC: "02:00:00:00:00:01", Address: "192.0.2.100"}, {ID: "id", ClientID: "0102", Address: "192.0.2.20"}}, []byte{1, 2}, "192.0.2.20"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := fixtureSettings()
			s.RangeEnd, s.Reservations = "192.0.2.101", tc.reservations
			dir := filepath.Join(t.TempDir(), "dhcp")
			store, rows, err := OpenLeaseStore(dir, s.MaxLeases, nil)
			require.NoError(t, err)
			link := newRuntimeLink()
			rt, err := StartRuntime(context.Background(), s, 1, link, quietProbe, store, rows)
			require.NoError(t, err)
			defer rt.Close(context.Background())
			link.input <- qualificationDiscovery(t, 2, nil).ToBytes()
			foreign := wireReceive(t, link)
			for _, reservation := range tc.reservations {
				assert.NotEqual(t, reservation.Address, foreign.YourIPAddr.String(), "reserved address offered to another client")
			}
			qualificationACK(t, link, foreign, nil)
			link.input <- qualificationDiscovery(t, 1, tc.id).ToBytes()
			owner := wireReceive(t, link)
			assert.Equal(t, tc.want, owner.YourIPAddr.String())
			qualificationACK(t, link, owner, tc.id)
			require.NoError(t, rt.Close(context.Background()))
			final, recovered, err := OpenLeaseStore(dir, s.MaxLeases, nil)
			require.NoError(t, err)
			defer final.Close(context.Background())
			require.Len(t, recovered.Leases, 2)
			for _, lease := range recovered.Leases {
				if lease.MAC[5] == 1 {
					assert.Equal(t, tc.want, lease.Address.String())
					if tc.id != nil {
						assert.Equal(t, "id:"+string(tc.id), lease.Identity)
					}
				}
			}
		})
	}
}

// StartRuntime does not expose a clock hook. Exercise its actual engine/store
// boundary directly so expiry and the longer ownership hold need no long sleep.
func TestAutomatedQualificationExpiryDeleteRestart(t *testing.T) {
	for _, point := range []string{"rolled-back-delete", "committed-before-completion", "completed-delete"} {
		t.Run(point, func(t *testing.T) {
			now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
			clock := func() time.Time { return now }
			s := fixtureSettings()
			s.RangeEnd, s.MaxLeases = s.RangeStart, 1
			dir := filepath.Join(t.TempDir(), "dhcp")
			store, _, err := OpenLeaseStore(dir, s.MaxLeases, clock)
			require.NoError(t, err)
			defer store.Close(context.Background())
			lease := storedLease(now, 1)
			lease.Address = netip.MustParseAddr(s.RangeStart)
			lease.Expiry, lease.HoldUntil = now.Add(time.Minute), now.Add(2*time.Minute)
			storePut(t, store, 1, lease)
			require.NoError(t, store.Close(context.Background()))
			store, recovery, err := OpenLeaseStore(dir, s.MaxLeases, clock)
			require.NoError(t, err)
			defer store.Close(context.Background())
			e, err := NewEngine(s, 2, clock)
			require.NoError(t, err)
			require.NoError(t, e.Restore(recovery.Leases, recovery.LastKnown))
			now = lease.Expiry.Add(time.Second)
			assert.Empty(t, e.Tick(), "expiry alone must not retire the longer hold")
			assert.Equal(t, Outcome{}, e.Handle(client(2, Discover)))
			now = lease.HoldUntil.Add(-time.Nanosecond)
			assert.Empty(t, e.Tick())
			now = lease.HoldUntil
			deletes := e.Tick()
			require.Len(t, deletes, 1)
			assert.Equal(t, DeleteLease, deletes[0].Kind)
			assert.Equal(t, Outcome{}, e.Handle(client(2, Discover)), "pending delete still owns the only address")
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			begin := store.begin
			store.begin = func() (leaseTransaction, error) {
				tx, err := begin()
				if err != nil {
					return nil, err
				}
				if point == "rolled-back-delete" {
					return qualificationRollbackTransaction{tx, entered, release}, nil
				}
				return faultTransaction{leaseTransaction: tx, before: func() error {
					close(entered)
					<-release
					return nil
				}}, nil
			}
			require.NoError(t, store.Submit(context.Background(), deletes[0]))
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("delete did not reach SQLite commit barrier")
			}
			assert.Equal(t, Outcome{}, e.Handle(client(2, Discover)), "SQLite delete has not committed")
			assert.Equal(t, []Lease{lease}, e.DurableLeases())
			unblock()
			result := storeResult(t, store)
			if point == "rolled-back-delete" {
				require.ErrorContains(t, result.Err, "qualification delete rollback")
				e.CompleteCommit(result)
				assert.Equal(t, Outcome{}, e.Handle(client(2, Discover)))
			} else {
				require.NoError(t, result.Err)
				if point == "completed-delete" {
					e.CompleteCommit(result)
					assert.NotNil(t, e.Handle(client(2, Discover)).Probe)
				}
			}
			require.NoError(t, store.Close(context.Background()))
			final, recovered, err := OpenLeaseStore(dir, s.MaxLeases, clock)
			require.NoError(t, err)
			defer final.Close(context.Background())
			fresh, err := NewEngine(s, 3, clock)
			require.NoError(t, err)
			require.NoError(t, fresh.Restore(recovered.Leases, recovered.LastKnown))
			if point == "rolled-back-delete" {
				require.Equal(t, []Lease{lease}, recovered.Leases)
				assert.Equal(t, Outcome{}, fresh.Handle(client(2, Discover)), "restart cannot bypass a failed durable delete")
				retry := fresh.Tick()
				require.Len(t, retry, 1)
				require.NoError(t, final.Submit(context.Background(), retry[0]))
				r := storeResult(t, final)
				require.NoError(t, r.Err)
				fresh.CompleteCommit(r)
			} else {
				assert.Empty(t, recovered.Leases)
			}
			assert.NotNil(t, fresh.Handle(client(2, Discover)).Probe, "committed delete must permit allocation after restart")
		})
	}
}
